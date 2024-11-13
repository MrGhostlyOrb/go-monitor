package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func hasAvaliableDiskSpace() bool {
	var stat unix.Statfs_t

	wd, err := os.Getwd()
	if err != nil {
		fmt.Println("[ERROR] unable to get working dir...")
		return false
	}

	err = unix.Statfs(wd, &stat)
	if err != nil {
		fmt.Println("[ERROR] unable to stat directory...")
		return false
	}

	if stat.Bavail*uint64(stat.Bsize) < 10000000000 {
		return false
	} else {
		return true
	}
}

func exitIfNoDiskSpace() {
	for {
		if !hasAvaliableDiskSpace() {
			fmt.Println("[MONITOR] disk space too low exiting...")
			os.Exit(0)
		} else {
			time.Sleep(time.Second)
		}
	}
}
