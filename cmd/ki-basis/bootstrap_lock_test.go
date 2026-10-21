package main

import "testing"

func TestBootstrapLockReleasesWithoutDeletingState(t *testing.T) {
	dir := t.TempDir()
	release, err := lockBootstrap(dir)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := lockBootstrap(dir); err == nil {
		duplicate()
		release()
		t.Fatal("concurrent bootstrap accepted")
	}
	release()
	release, err = lockBootstrap(dir)
	if err != nil {
		t.Fatal("lock cannot be reacquired", err)
	}
	release()
}
