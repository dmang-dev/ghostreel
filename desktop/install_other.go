// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package main

// On macOS and Linux the app finds new releases but doesn't install them:
// the viewer links to the release page instead.

func installRole() string { return "" }

func install(string) error { return installErr("can't install updates on this system") }

func cleanUpOldExe() {}
