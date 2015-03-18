package librestarter

import (
	"io"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func getExitCode(err error) int {
	if exiterr, ok := err.(*exec.ExitError); ok {
		// The program has exited with an exit code != 0

		// This works on both Unix and Windows. Although package
		// syscall is generally platform dependent, WaitStatus is
		// defined for both Unix and Windows and in both cases has
		// an ExitStatus() method with the same signature.
		if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
		return -1
	}
	return 0
}

func Bold(str string) string {
	if runtime.GOOS == "windows" {
		return str
	}
	return "\033[1m" + str + "\033[0m"
}

func modifyIfEmpty(a *string, b string) {
	if *a == "" {
		*a = b
	}
}

func probablyModifyInt(a *int, question bool, newval int) {
	if question {
		*a = newval
	}
}

func modifyIfNilDuration(ptrptr **time.Duration, val time.Duration) {
	if *ptrptr == nil {
		*ptrptr = &val
	}
}
func modifyIfZeroDuration(ptrptr *time.Duration, val time.Duration) {
	if *ptrptr == time.Duration(0) {
		*ptrptr = val
	}
}
func modifyIfNilBool(ptrptr **bool, val bool) {
	if *ptrptr == nil {
		*ptrptr = &val
	}
}

func WriteAllBytes(data []byte, writer io.Writer) error {
	n, err := writer.Write(data)
	if err != nil {
		return err
	}
	dataSize := len(data)
	for i := n; i < dataSize; i += n {
		n, err = writer.Write(data[i:])
		if err != nil {
			return err
		}
	}
	return nil
}

func replaceAll(source string, pairs ...string) string {

	for i := 0; i < len(pairs)/2; i++ {
		source = strings.Replace(source, pairs[i*2], pairs[i*2+1], -1)
	}
	return source
}

func exeNameFromPath(appname string) string {
	if lia := strings.LastIndexAny(appname, "/\\"); lia > -1 {
		appname = appname[lia+1:]
	}
	return appname
}
