package librestarter

import (
	"bufio"
	"crypto/tls"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	. "github.com/daviddengcn/go-colortext"
	"github.com/rshmelev/gologs/libgologs"

	"github.com/rshmelev/go-inthandler"
)

//                            Trace Debug Info   Warn    Err  Critical
var LogLevelToColor = []Color{Blue, Cyan, White, Yellow, Red, Magenta}

type RestarterOptions struct {
	RestartRestTime         time.Duration
	ShutdownURL             string
	StdErrToStdOut          bool
	MaxTimeToWaitForCleanup *time.Duration
	Stop                    *bool
	StopChannel             chan struct{}
	RestarterPrefix         string
	ColorizerPrefix         string
	LogLevelStrOffset       int
	LogToParam              string
}

var mode = "std"

// if "__phoenix__" param is found across options then app
// is entering "restarter" (supervisor) mode and starts itself without that flag
// (to avoid recursive starting of restarters)
func ProbablyBecomeRestarter(opts RestarterOptions) {
	args := os.Args
	pos := 0

	setDefaultValues(&opts) // LogLevelStrOffset,RestarterPrefix,ColorizerPrefix

	restarterparam := ""
	// HasPrefix is used because param can also have some options in suffix
	for i, v := range args {
		// first, be restarter.
		// restarted app will be colorizer, if will be.
		if strings.HasPrefix(v, opts.RestarterPrefix) {
			mode = "restarter"
			restarterparam = v[len(opts.RestarterPrefix):]
			pos = i
			break
		}
		if strings.HasPrefix(v, opts.ColorizerPrefix) {
			restarterparam = v[len(opts.ColorizerPrefix):]
			mode = "colorizer"
			pos = i
			break
		}
	}

	if mode != "std" {
		args = append(args[:pos], args[pos+1:]...) // prepare params for child
		beSupervisor(args, opts, restarterparam, mode)
		os.Exit(0)
	}
}

var OriginalTimeToWaitForCleanup time.Duration

type ByteData struct {
	stderr bool
	data   []byte
}

var newlineBytes = []byte{byte('\n')}
var verbose bool

func beSupervisor(args []string, opts RestarterOptions, param string, mode string) {
	colorize := mode == "colorizer"
	verbose = !colorize

	// small additional piece of time
	// reason: do not die before child process
	someDelay := time.Millisecond * 150

	modifyIfNilDuration(&opts.MaxTimeToWaitForCleanup, time.Second*5+someDelay*2) // +2x
	modifyIfNilBool(&opts.Stop, false)
	modifyIfZeroDuration(&opts.RestartRestTime, time.Second*1)

	OriginalTimeToWaitForCleanup = *(opts.MaxTimeToWaitForCleanup) - someDelay // +x

	// by default - work with localhost
	opts.ShutdownURL = replaceAll(opts.ShutdownURL,
		"//:", "//localhost:",
		"//0.0.0.0:", "//localhost:",
		"//*:", "//localhost:")

	gointhandler.TakeCareOfInterrupts(true)

	// __phoenix:logto=/logs/dailyrotating.log
	logto := ""
	if strings.HasPrefix(param, opts.LogToParam) {
		logto = param[len(opts.LogToParam):]
		// empty param means "do not log to file, i'm taking care of it"
		args = append(args, "--logto=")
	}

	var logrotator io.WriteCloser
	if logto != "" {
		logrotator = libgologs.CreateDailyRotatingWriteCloser(logto, 500)
	}

	appname := strings.Replace(exeNameFromPath(args[0]), ".exe", "", 1)
	logprefix := Bold(appname+"-"+mode) + ": "

	for !*opts.Stop {
		if verbose {
			log.Println(logprefix + "starting child process: " + sliceToCmdStr(args))
			println("")
		}
		childstart := time.Now()

		cmd := exec.Command(args[0], args[1:]...)
		cmdstdout, e1 := cmd.StdoutPipe()
		cmdstderr, e2 := cmd.StderrPipe()

		if logto != "" {
			go logAndRotate(cmdstdout, cmdstderr, logrotator)
		} else if colorize {
			go logAndColorize(cmdstdout, cmdstderr, opts)
		} else { // most common mode when everything should just go through app
			if e1 == nil {
				go io.Copy(os.Stdout, cmdstdout)
			}
			if e2 == nil {
				if !opts.StdErrToStdOut {
					go io.Copy(os.Stderr, cmdstderr)
				} else {
					go io.Copy(os.Stderr, cmdstdout) //?... Stdout, cmdstderr)
				}
			}
		}

		cmdStopChan := runAsync_andGetErrorResultChan(func() error { return cmd.Run() })

		select {
		case code := <-cmdStopChan:
			time.Sleep(20 * time.Millisecond) // hopefully that will be enough to read all logs...
			println("")                       // pretty empty line
			log.Println(logprefix+"child process exited with code ", getExitCode(code),
				" after "+time.Since(childstart).String())

		case <-opts.StopChannel:
			log.Println(logprefix + "sending signal to child process...")
			p := cmd.Process
			if p != nil {
				stopNoPanic(cmd, opts.ShutdownURL)
			}
		}

		if !*opts.Stop { // application wasn't stopped, so rest a bit and restart!
			time.Sleep(opts.RestartRestTime)
		}
	}

	if verbose {
		log.Println(logprefix + "stopped")
	}
}

// read data from reader and put it into the channel
func readToChannel(a chan *ByteData, reader io.ReadCloser, isStderr bool) {
	defer func() { a <- nil }()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		a <- &ByteData{isStderr, scanner.Bytes()}
	}
}

// get channel that is filled with stdout and stderr data in background
func getLogsChannel(stdout, stderr io.ReadCloser) chan *ByteData {
	ch := make(chan *ByteData, 100000)
	go readToChannel(ch, stdout, false)
	go readToChannel(ch, stderr, true)
	return ch
}

// singlethreaded logs processing
func logAndRotate(o, e io.ReadCloser, logrotator io.WriteCloser) {
	ch := getLogsChannel(o, e)
	i := 0
	currentDay := -1
	currentDate := "-"
	currentDayBytes := []byte(currentDate)
	for {
		p := <-ch
		if p == nil {
			i++
			// stderr and stdout are closed (see func readToChannel)
			if i == 2 {
				return
			}
			continue
		}

		now := time.Now().UTC()
		if now.Day() != currentDay {
			currentDate = now.Format("2006-01-02 ")
			currentDayBytes = []byte(currentDate)
		}

		if len(p.data) > 0 {
			if len(p.data) > 16 && (p.data[0] >= '0' && p.data[0] <= '9') && p.data[2] == ':' {
				WriteAllBytes(currentDayBytes, logrotator)
			}
			WriteAllBytes(p.data, logrotator)
		}
		WriteAllBytes(newlineBytes, logrotator)
		println(string(p.data))
	}
}

// colorizer is simply colorizing
func logAndColorize(o, e io.ReadCloser, opts RestarterOptions) {
	ch := getLogsChannel(o, e)
	i := 0
	for {
		p := <-ch
		if p == nil {
			i++
			// stderr and stdout are closed (see func readToChannel)
			if i == 2 {
				return
			}
			continue
		}

		// try to determine log level
		var level int = libgologs.LLEVEL_INFO
		if len(p.data) > opts.LogLevelStrOffset {
			_, level = libgologs.DetectLevelAndCutPrefix(p.data[opts.LogLevelStrOffset:], false)
		}

		if len(p.data) == 0 {
			println("")
		} else {
			if p.data[0] < '0' || p.data[0] > '9' {
				level = libgologs.LLEVEL_DEBUG
			}
			if level >= 0 {
				ChangeColor(LogLevelToColor[level], level != libgologs.LLEVEL_INFO, None, false)
			}
			println(string(p.data))
			if level >= 0 {
				ResetColor()
			}
		}
	}
}

//----------------------------------------- aux

// proper stopping of the child
// 1) attempt to send http request
// 2) attempt to kill via interrupt
// 3) attempt to force kill using cmd.Process.Kill()
// function blocks while process is alive
func stopNoPanic(cmd *exec.Cmd, HttpRestartUrl string) {
	defer func() { recover() }()
	go cmd.Process.Signal(os.Interrupt)
	if HttpRestartUrl != "" && HttpRestartUrl != "-" {
		go GetHttpContents(HttpRestartUrl)
	}
	ch := make(chan int, 2)
	go func() {
		defer func() { recover() }()
		time.Sleep(OriginalTimeToWaitForCleanup)
		cmd.Process.Kill()
		ch <- 2
	}()
	go func() {
		defer func() { recover() }()
		cmd.Process.Wait()
		ch <- 1
	}()

	// wait for some event to happen quickly
	how := <-ch
	if how == 2 {
		log.Println("forced killing of child process...")
	}
}

// used just for making http request. Response is ignored
func GetHttpContents(url string) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := http.Client{
		Transport: tr,
	}
	r, err := client.Get(url)
	defer func() {
		if r != nil && r.Body != nil {
			r.Body.Close()
		}
	}()

	if err != nil {
		return
	}

	ioutil.ReadAll(r.Body)
}

// used to prepare command line with params for starting the application
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
			switch runtime.GOOS {
			case "windows":
				v = "\"" + strings.Replace(v, "\"", "\\\"", -1) + "\""
			default:
				v = "\"" + strings.Replace(v, "\"", "\"\"", -1) + "\""
			}
		}
		s += " " + v
	}
	return s
}

func setDefaultValues(opts *RestarterOptions) {
	switch opts.LogLevelStrOffset {
	case 0:
		opts.LogLevelStrOffset = 13 // "11:22:33.999 "
	case -1:
		opts.LogLevelStrOffset = 0
	}

	modifyIfEmpty(&opts.RestarterPrefix, "__phoenix")
	modifyIfEmpty(&opts.ColorizerPrefix, "colorize")
	modifyIfEmpty(&opts.LogToParam, ":logto=")

}

/*
	itStopped := make(chan int, 1)

	var code error
	go func() {
		code = cmd.Run()
		itStopped <- 1
	}()
*/

func runAsync_andGetErrorResultChan(f func() error) chan error {
	itStopped := make(chan error, 1)
	go func() {
		res := f()
		itStopped <- res
	}()

	return itStopped
}
