package main

import (
	"errors"
	"flag"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"os"
	"path/filepath"
	"time"
)

type windowsService struct{ args []string }

func (s windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	for i, arg := range s.args {
		if arg == "--config" && i+1 < len(s.args) {
			if err := os.Chdir(filepath.Dir(s.args[i+1])); err != nil {
				return true, 1
			}
		}
	}
	os.Args = append([]string{os.Args[0]}, s.args...)
	done := make(chan error, 1)
	go func() { done <- run() }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				stopService()
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				checkpoint := uint32(1)
				for {
					status <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: 120000}
					select {
					case err := <-done:
						if err != nil {
							return true, 1
						}
						return false, 0
					case <-ticker.C:
						checkpoint++
					}
				}
			}
		}
	}
}

// Install prepares an auto-start virtual service account. Directory ACLs and model
// access are explicit operator configuration; the installer does not elevate it.
func serviceCommandMain(args []string) error {
	if len(args) == 0 {
		return errors.New("service install|run requires a name and command")
	}
	action := args[0]
	flags := flag.NewFlagSet("service", flag.ContinueOnError)
	name := flags.String("name", "KI-Basis-Gateway", "Windows service name")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	command := flags.Args()
	if !configServiceName(*name) {
		return errors.New("invalid service name")
	}
	if action == "run" {
		if len(command) == 0 {
			return errors.New("service command missing")
		}
		return svc.Run(*name, windowsService{command})
	}
	if action != "install" || len(command) == 0 || (command[0] != "serve" && command[0] != "agent-ui" && command[0] != "agent") {
		return errors.New("service install requires serve, agent-ui or agent run")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	// Require absolute paths: SCM starts services in the Windows system directory.
	for i, arg := range command {
		if arg == "--config" || arg == "--dir" || arg == "--state" {
			if i+1 >= len(command) || !filepath.IsAbs(command[i+1]) {
				return errors.New("service paths must be absolute")
			}
		}
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	serviceArgs := append([]string{"service", "run", "--name", *name, "--"}, command...)
	s, err := m.CreateService(*name, executable, mgr.Config{DisplayName: *name, StartType: mgr.StartAutomatic, ServiceStartName: `NT SERVICE\` + *name, SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED, Description: "KI-Basis unattended gateway/model agent"}, serviceArgs...)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return err
	}
	return s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}, {Type: mgr.ServiceRestart, Delay: 10 * time.Second}, {Type: mgr.ServiceRestart, Delay: 30 * time.Second}}, 86400)
}
