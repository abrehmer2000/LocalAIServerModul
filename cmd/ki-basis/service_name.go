package main

import "regexp"

func configServiceName(name string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`).MatchString(name)
}
