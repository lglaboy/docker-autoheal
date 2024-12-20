package main

import (
	"testing"
	"time"
)

func TestRestartRecords(t *testing.T) {
	records := &RestartRecords{}
	records.Add("123", 1, time.Now())
	if !records.Check("123") {
		t.Errorf("Expected to find container 123")
	}
}
