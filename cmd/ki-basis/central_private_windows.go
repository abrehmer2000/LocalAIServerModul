package main

import (
	"errors"
	"golang.org/x/sys/windows"
)

func protectCentralDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("cannot identify central directory owner")
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return errors.New("cannot construct central directory ACL")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return errors.New("cannot read central directory ACL")
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return errors.New("cannot protect central directory")
	}
	return nil
}
