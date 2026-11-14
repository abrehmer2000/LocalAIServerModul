package main

import (
	"errors"
	"strings"
	"testing"
)

func TestGUIErrorDoesNotExposeUnknownErrorDetails(t *testing.T) {
	private := "unexpected response containing kb_private_customer_key"
	message := commandErrorMessage(errors.New(private))
	if strings.Contains(message, private) || strings.Contains(message, "kb_private") {
		t.Fatal("graphical fallback disclosed private error details")
	}
	if !strings.Contains(message, "Startmenü") {
		t.Fatal("graphical fallback must offer an actionable recovery step")
	}
}
