package main

import "golang.org/x/sys/windows/svc"

func desktopServiceManager() bool {
	service, err := svc.IsWindowsService()
	return err == nil && service
}
