//go:build !linux

package main

import (
	"os"
	"syscall"
	"time"
)

func embySysProcAttr() *syscall.SysProcAttr { return nil }

func signalProcessGroup(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

func processGroupAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func prepareChildSupervisor() error { return nil }

func reapProcessGroup(_ int, _ time.Duration) {}
