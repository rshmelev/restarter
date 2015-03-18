package main

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const author = "rshmelev@gmail.com"
const version = "1.0.1"

var appname string = "restarter"

var ever bool = true
var running chan int = make(chan int, 1)
var args []string = os.Args[1:]
var cmd *exec.Cmd
var appstart time.Time = time.Now()
var logprefix string = Bold(appname) + ": "
var restartSleepSeconds time.Duration = 1
var killWaitSeconds time.Duration = 1
var stdoutAndStderr bool = true

var mutex sync.Mutex

func main() {

	parseArgs()

	// help
	if len(args) == 0 {
		println("Version: " + Bold(version) + ", Author: " + Bold(author))
		println(Bold("Usage: ") + appname + " [options] <path/to/executable> [<param1> <param2> ....]")
		println(Bold("Options:"))
		println("-restartresttime=<seconds>  <- how long to wait after child process death before restarting it again")
		println("-waitfordeath=<seconds>  <- how long to wait for child process death after sending the signal")
		println("-stderrtostdout  <- to separate restarter and child outputs")
		println("*you can also use -r=<seconds> -w=<seconds> and -s ")
		return
	}

	// will reset ever and kill child process in case Interrupt signal will be received
	takeCareAboutInterrupts()

	for ever {

		println(logprefix + "starting child process:" + sliceToCmdStr(args))
		println("")
		childstart := time.Now()

		cmd = exec.Command(args[0], args[1:]...)
		cmdstdout, e1 := cmd.StdoutPipe()
		cmdstderr, e2 := cmd.StderrPipe()
		if e1 == nil {
			go io.Copy(os.Stdout, cmdstdout)
		}
		if e2 == nil {
			if stdoutAndStderr {
				go io.Copy(os.Stderr, cmdstderr)
			} else {
				go io.Copy(os.Stdout, cmdstderr)
			}
		}
		// cmd.Stdout = os.Stdout
		// cmd.Stderr = os.Stderr

		running <- 1

		code := cmd.Run()
		time.Sleep(20 * time.Millisecond) // hopefully that will be enough to read all logs...

		<-running

		println("")
		println(logprefix+"child process exited with code", getExitCode(code), "after "+time.Since(childstart).String())

		if ever {
			time.Sleep(time.Second * restartSleepSeconds)
		}
	}

	println(logprefix + "total execution time: " + time.Since(appstart).String())
}

func takeCareAboutInterrupts() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		defer func() { recover(); running <- 1 }()
		for sig := range c {
			println("")
			println("-----------------------------------------------------")
			println(logprefix + "received signal: " + sig.String())
			ever = false

			select {
			case <-running:
			default:
				os.Exit(0)
			}

			println(logprefix + "sending signal to child process...")
			cmd.Process.Signal(os.Interrupt)

			time.Sleep(killWaitSeconds * time.Second)
			cmd.Process.Kill()

			running <- 1
		}
	}()
}

func parseArgs() {
	for len(args) > 0 {
		p := args[0]
		if p[0] != '-' {
			return
		}

		// for long params like --objects-count=10
		if len(p) > 1 && p[1] == '-' {
			p = p[1:]
		}

		args = args[1:]
		if p[:2] == "-r" {
			restartSleepSeconds = time.Duration(extractInt(p))
		}
		if p[:2] == "-w" {
			killWaitSeconds = time.Duration(extractInt(p))
		}
		if p[:2] == "-s" {
			stdoutAndStderr = false
		}
	}
}

func extractInt(s string) int {
	ss := strings.Split(s, "=")
	if len(ss) < 2 {
		return 0
	}
	c, _ := strconv.Atoi(ss[1])
	return c
}

//----------------------------------------- aux

func sliceToCmdStr(args []string) string {
	s := ""
	for _, v := range args {
		hasspace := false
		for _, v2 := range v {
			if v2 == ' ' {
				hasspace = true
				break
			}
		}
		if hasspace {
			v = "\"" + v + "\""
		}
		s += " " + v
	}
	return s
}

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
