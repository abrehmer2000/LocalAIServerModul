package main

import "testing"

func TestRemoteDiagnosticsNeverReturnRawRemoteMessages(t *testing.T) {
	raw := []byte(`untrusted banner
KI_BASIS_SETUP:{"error":"setup_checks_failed","checks":[{"id":"connection","state":"error","message":"SECRET"},{"id":"unknown","state":"error","message":"SECRET"}]}`)
	failure, ok := parseBootstrapFailure(raw)
	if !ok || len(failure.Checks) != 1 || failure.Checks[0].Message != remoteCheckMessages["connection"] {
		t.Fatalf("unsafe or missing diagnostics: %#v", failure)
	}
	for _, raw := range []string{`KI_BASIS_SETUP:{"error":"remote_arbitrary_secret"}`, `KI_BASIS_SETUP:{bad json}`} {
		if _, ok := parseBootstrapFailure([]byte(raw)); ok {
			t.Fatal("untrusted error accepted")
		}
	}
}
