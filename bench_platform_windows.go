//go:build windows

package main

import (
	"fmt"
	"math/bits"
	"runtime"
	"syscall"
	"unsafe"
)

// Windows measurement platform. Two problems were measured on the development
// laptop (Intel Core Ultra 7 255H, see ANOMALIES.md):
//   - Go's monotonic clock on Windows advances once per timer interrupt
//     (~1 ms), so µs-scale timings are quantized; QueryPerformanceCounter is not.
//   - On a hybrid CPU, Windows power throttling (EcoQoS) moves a background
//     console process onto efficiency cores up to 3x slower, which made two
//     back-to-back measurements of the same block differ by up to x2.6.
var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
	procGetCurrentProcess         = kernel32.NewProc("GetCurrentProcess")
	procSetProcessInformation     = kernel32.NewProc("SetProcessInformation")
	procSetPriorityClass          = kernel32.NewProc("SetPriorityClass")
	procSetProcessAffinityMask    = kernel32.NewProc("SetProcessAffinityMask")
	procGetSystemCpuSetInfo       = kernel32.NewProc("GetSystemCpuSetInformation")

	qpcFrequency = queryPerformanceFrequency()
)

const (
	processPowerThrottling         = 4    // PROCESS_INFORMATION_CLASS value
	powerThrottlingExecutionSpeed  = 0x1  // PROCESS_POWER_THROTTLING_EXECUTION_SPEED
	highPriorityClass              = 0x80 // HIGH_PRIORITY_CLASS
	cpuSetInformationType          = 0    // CpuSetInformation
	cpuSetGroupOffset              = 12   // SYSTEM_CPU_SET_INFORMATION.CpuSet.Group
	cpuSetLogicalIndexOffset       = 14   // .CpuSet.LogicalProcessorIndex
	cpuSetEfficiencyClassOffset    = 18   // .CpuSet.EfficiencyClass
	cpuSetMinimumEntrySize         = 19
	maxLogicalProcessorsInOneGroup = 64
)

// nowNanos reads QueryPerformanceCounter in nanoseconds.
func nowNanos() int64 {
	var ticks int64
	procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&ticks)))
	return ticks/qpcFrequency*1e9 + ticks%qpcFrequency*1e9/qpcFrequency
}

func queryPerformanceFrequency() int64 {
	var freq int64
	procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&freq)))
	return freq
}

// prepareProcess opts out of power throttling, raises the priority class and,
// on a hybrid CPU, pins the process to its most performant core class so every
// algorithm runs on the same kind of core. It returns a one-line description.
func prepareProcess() string {
	process, _, _ := procGetCurrentProcess.Call()
	state := struct{ Version, ControlMask, StateMask uint32 }{1, powerThrottlingExecutionSpeed, 0}
	procSetProcessInformation.Call(process, processPowerThrottling, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	procSetPriorityClass.Call(process, highPriorityClass)

	desc := "timer QueryPerformanceCounter, power throttling off, high priority"
	mask, err := performanceCoreMask()
	switch {
	case err != nil:
		return desc + fmt.Sprintf(", core detection failed (%v), not pinned", err)
	case mask == 0:
		return desc + ", homogeneous cores, not pinned"
	}
	if r, _, err := procSetProcessAffinityMask.Call(process, uintptr(mask)); r == 0 {
		return desc + fmt.Sprintf(", pinning failed (%v)", err)
	}
	runtime.GOMAXPROCS(bits.OnesCount64(mask))
	return desc + fmt.Sprintf(", pinned to %d performance cores (mask %#x), GOMAXPROCS=%d",
		bits.OnesCount64(mask), mask, runtime.GOMAXPROCS(0))
}

// performanceCoreMask returns the affinity mask of the logical processors in
// the highest EfficiencyClass, or 0 when all cores share one class (nothing to
// pin) or the machine spans several processor groups.
func performanceCoreMask() (uint64, error) {
	buf, err := systemCPUSetInformation()
	if err != nil {
		return 0, err
	}
	classOf := map[uint8]uint8{} // logical processor -> efficiency class
	maxClass, minClass := uint8(0), uint8(255)
	for off := 0; off+cpuSetMinimumEntrySize <= len(buf); {
		size := int(*(*uint32)(unsafe.Pointer(&buf[off])))
		kind := *(*uint32)(unsafe.Pointer(&buf[off+4]))
		if size == 0 {
			break
		}
		if kind == cpuSetInformationType {
			if group := *(*uint16)(unsafe.Pointer(&buf[off+cpuSetGroupOffset])); group != 0 {
				return 0, nil // multi-group machine: leave scheduling to Windows
			}
			class := buf[off+cpuSetEfficiencyClassOffset]
			classOf[buf[off+cpuSetLogicalIndexOffset]] = class
			maxClass, minClass = max(maxClass, class), min(minClass, class)
		}
		off += size
	}
	if maxClass == minClass {
		return 0, nil
	}
	var mask uint64
	for cpu, class := range classOf {
		if class == maxClass && cpu < maxLogicalProcessorsInOneGroup {
			mask |= 1 << cpu
		}
	}
	return mask, nil
}

// systemCPUSetInformation returns the raw SYSTEM_CPU_SET_INFORMATION array.
func systemCPUSetInformation() ([]byte, error) {
	var needed uint32
	procGetSystemCpuSetInfo.Call(0, 0, uintptr(unsafe.Pointer(&needed)), 0, 0)
	if needed == 0 {
		return nil, fmt.Errorf("GetSystemCpuSetInformation returned no size")
	}
	buf := make([]byte, needed)
	if r, _, err := procGetSystemCpuSetInfo.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(needed),
		uintptr(unsafe.Pointer(&needed)), 0, 0); r == 0 {
		return nil, fmt.Errorf("GetSystemCpuSetInformation: %w", err)
	}
	return buf[:needed], nil
}
