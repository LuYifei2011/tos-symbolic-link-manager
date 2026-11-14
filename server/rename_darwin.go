package main

import (
	"golang.org/x/sys/unix"
	"time"
)

func renameNoReplace(from int, source string, to int, destination string) error {
	return unix.RenameatxNp(from, source, to, destination, unix.RENAME_EXCL)
}

func statModified(st *unix.Stat_t) time.Time { return time.Unix(st.Mtim.Sec, st.Mtim.Nsec) }
