package agent

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"sync"
	"unsafe"
)

var processJobs sync.Map             // *exec.Cmd -> owned Job Object handle
func configureProcess(cmd *exec.Cmd) {}
func startProcess(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	if err = cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return err
	}
	processJobs.Store(cmd, job)
	return nil
}
func waitProcess(cmd *exec.Cmd) error {
	err := cmd.Wait()
	if job, ok := processJobs.LoadAndDelete(cmd); ok {
		windows.CloseHandle(job.(windows.Handle))
	}
	return err
}
func killProcessTree(cmd *exec.Cmd) error {
	if job, ok := processJobs.Load(cmd); ok {
		return windows.TerminateJobObject(job.(windows.Handle), 1)
	}
	if cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}
