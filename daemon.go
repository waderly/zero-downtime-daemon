/*
* Copyright (c) 2013, PinIdea Co. Ltd.
* Tomasen <tomasen@gmail.com> & Reck Hou <reckhou@gmail.com>
* All rights reserved.
* Redistribution and use in source and binary forms, with or without
* modification, are permitted provided that the following conditions are met:
*
*     * Redistributions of source code must retain the above copyright
*       notice, this list of conditions and the following disclaimer.
*     * Redistributions in binary form must reproduce the above copyright
*       notice, this list of conditions and the following disclaimer in the
*       documentation and/or other materials provided with the distribution.
*
* THIS SOFTWARE IS PROVIDED BY THE REGENTS AND CONTRIBUTORS "AS IS" AND ANY
* EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
* WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
* DISCLAIMED. IN NO EVENT SHALL THE COMPANY AND CONTRIBUTORS BE LIABLE FOR ANY
* DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
* (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
* LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND
* ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
* (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
* SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
*/

/* The idea is came from nginx and this post: http://blog.nella.org/?p=879
* and code here: http://code.google.com/p/jra-go/source/browse/#hg%2Fcmd%2Fupgradable
* explain here: http://stackoverflow.com/questions/5345365/how-can-nginx-be-upgraded-without-dropping-any-requests
*/

package gozd

import (
  "os"
  "io"
  "fmt"
  "net"
  "log"
  "sync"
  "errors"
  "unsafe"
  "reflect"
  "syscall"
  "runtime"
  "io/ioutil"
  "os/signal"
  "crypto/sha1"
  "encoding/json"
  "path/filepath"
  "./osext" //  "bitbucket.org/kardianos/osext/src"
)

var (
  cx_ chan bool = make(chan bool,1)
  wg_ sync.WaitGroup
  hash_ string
  confs_ map[string]Conf = make(map[string]Conf)
)

// https://codereview.appspot.com/7392048/#ps1002
func findProcess(pid int) (p *os.Process, err error) {
  if e := syscall.Kill(pid, syscall.Signal(0)); pid <= 0 || e != nil {
  	return nil, fmt.Errorf("find process %v", e)
  }
  p = &os.Process{Pid: pid}
  runtime.SetFinalizer(p, (*os.Process).Release)
  return p, nil
}

func infopath() string {
  h := sha1.New()
  io.WriteString(h, hash_)
  return os.TempDir() + fmt.Sprintf("gozd%x.json", h.Sum(nil))
}

func abdicate() {
  // i'm not master anymore
  os.Remove(infopath())
}

func masterproc() (p *os.Process, err error) {
  file, err := ioutil.ReadFile(infopath())
  if err != nil {
    return
  }

  var pid int
  err = json.Unmarshal(file, &pid)
  if err != nil {
    return
  }
  
  p, err = findProcess(pid)
  return 
}

func writepid() (err error) {
  
  var p = os.Getpid()
  
  b, err := json.Marshal(p)
	if err != nil {
		return
	}
  err = ioutil.WriteFile(infopath(), b, 0666)
  return
}

// distinguish master/worker/shuting down process
// see: http://stackoverflow.com/questions/14926020/setting-process-name-as-seen-by-ps-in-go
func setProcessName(name string) error {
    argv0str := (*reflect.StringHeader)(unsafe.Pointer(&os.Args[0]))
    argv0 := (*[1 << 30]byte)(unsafe.Pointer(argv0str.Data))[:argv0str.Len]

    n := copy(argv0, name)
    if n < len(argv0) {
            argv0[n] = 0
    }

    return nil
}

// release all the listened port or socket
// wait all clients disconnect
// send signal to let caller do cleanups and exit
func shutdown() {

  log.Println("shutting down (pid):", os.Getpid())
  // shutdown process safely
  for _,conf := range confs_ {
    conf.l.Stop()
  }
  
  execpath, _ := osext.Executable()
	_, basename := filepath.Split(execpath)
  setProcessName("(shutting down)"+basename)
  
  wg_.Wait()
  
  cx_ <- true
}

func signalHandler() {
  // this is singleton by process
  // should not be called more than once!
  c := make(chan os.Signal, 1)
  signal.Notify(c, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR2, syscall.SIGINT)
  // Block until a signal is received.
  for s := range c {
    log.Println("signal received: ", s)
    switch (s) {
      case syscall.SIGHUP, syscall.SIGUSR2:
        // restart / fork and exec
        err := reload()
        if err != nil {
          log.Println("reload err:", err)
        }
        return

      case syscall.SIGTERM, syscall.SIGINT:
        abdicate()
        shutdown()
        return
    }
  }
}


type Conf struct {
    //mode              string // eg: http/fcgi/https
    Network, Address  string // eg: unix/tcp, socket/ip:port. see net.Dial
    //key, cert         string // for https only
    //serv
    l *stoppableListener
    Fd uintptr
}

type Context struct {
  Hash    string // config path in most case
  Signal  string
  Logfile string
  Servers map[string]Conf
}

func validCtx(ctx Context) error {
  if (len(ctx.Hash) <= 1) {
    return errors.New("ctx.Hash is too short")
  }
 
  if (len(ctx.Servers) <= 0) {
    return errors.New("ctx.Servers is empty")
  }
  
  return nil
}

func equavalent(a Conf, b Conf) bool {
  return (a.Network == b.Network && a.Address == b.Address)
}

func reload() (err error) {
  // fork and exec / restart
  execpath, err := osext.Executable()
  if err != nil {
    return
  }
  
	wd, err := os.Getwd()
	if nil != err {
		return
	}

  abdicate()
    
  // write all the fds into a json string
  // from beego, code is evil but much simpler than extend net/*
  allFiles := []*os.File{os.Stdin, os.Stdout, os.Stderr}
  
  for k,conf := range confs_ {
   v := reflect.ValueOf(conf.l.Listener).Elem().FieldByName("fd").Elem()
	 fd := uintptr(v.FieldByName("sysfd").Int())
   conf.Fd = uintptr(len(allFiles))
   confs_[k] = conf
   allFiles = append(allFiles, os.NewFile(fd, string(v.FieldByName("sysfile").String())))
  }
  
  b, err := json.Marshal(confs_)
	if err != nil {
		return
	}
  inheritedinfo := string(b)

	p, err := os.StartProcess(execpath, os.Args, &os.ProcAttr{
		Dir:   wd,
		Env:   append(os.Environ(), fmt.Sprintf("GOZDVAR=%v", inheritedinfo)),
		Files: allFiles,
	})
	if nil != err {
		return 
	}
	log.Printf("child %d spawned, parent: %d\n", p.Pid, os.Getpid())
  
  // exit since process already been forked and exec 
  shutdown()
  
  return
}

func initListeners(s map[string]Conf, cl chan net.Listener) error {
  // start listening 
  for k,c := range s {
    if c.Network == "unix" {
      os.Remove(c.Address)
    }
    listener, e := net.Listen(c.Network, c.Address)
    if e != nil {
      // handle error
      log.Println("bind() failed on:", c.Network, c.Address, "error:", e)
      continue
    }
    if c.Network == "unix" {
      os.Chmod(c.Address, 0666)
    }
    
    conf := s[k]
    sl := newStoppable(listener, &wg_)
    conf.l = sl
    confs_[k] = conf
    if cl != nil {
      cl <- sl
    }
  }
  if len(confs_) <= 0{
    return errors.New("interfaces binding failed completely")
  }
  
  return nil
}

func Daemonize(ctx Context, cl chan net.Listener) (c chan bool, err error) {
  
  err = validCtx(ctx)
  if err != nil {
    return
  }
  
  c = cx_
  hash_ = ctx.Hash
  
  // redirect log output, if set
  if len(ctx.Logfile) > 0 {
    f, e := os.OpenFile(ctx.Logfile, os.O_WRONLY | os.O_APPEND | os.O_CREATE, os.ModeAppend | 0666)
    if e != nil {
      err = e
      return
    }
    log.SetOutput(f)
  }
  
  inherited := os.Getenv("GOZDVAR");
  if len(inherited) > 0 {
    
    // this is the daemon
    // create a new SID for the child process
    s_ret, e := syscall.Setsid()
    if e != nil {
      err = e
      return
    }
    
    if s_ret < 0 {
      err = fmt.Errorf("Set sid failed %d", s_ret)
      return
    }
    
    // handle inherited fds
    heirs := make(map[string]Conf)
    err = json.Unmarshal([]byte(inherited), &heirs)
    if err != nil {
      return 
    }

    
    for k,heir := range heirs {
      // compare heirs and ctx confs
      conf, ok := ctx.Servers[k];
      if !ok || !equavalent(conf, heir) {
        // do not add the listener that already been removed
        continue
      }
      
    	f := os.NewFile(heir.Fd, k) 
    	l, e := net.FileListener(f)
    	if e != nil {
        err = e
        f.Close()
        log.Println("inherited listener binding failed", heir.Fd, "for", k, e)
    		continue 
    	}
      heir.l = newStoppable(l, &wg_)
      delete(ctx.Servers, k)
      confs_[k] = heir
      if cl != nil {
        cl <- heir.l
      }
    }
    
    if (len(confs_) <= 0 && err != nil) {
      return
    }
    
    // add new listeners
    err = initListeners(ctx.Servers, cl)
    if err != nil {
      return
    }
    
    // write process info
    err = writepid()
    if err != nil {
      return 
    }

    // Handle OS signals
    // Set up channel on which to send signal notifications.
    // We must use a buffered channel or risk missing the signal
    // if we're not ready to receive when the signal is sent.
    go signalHandler()
        
    return
  }
  
  // handle reopen or stop command
  proc, err := masterproc()
  switch (ctx.Signal) {
  case "stop","reopen","reload":
    if err != nil {
      return
    }
    if (ctx.Signal == "stop") {
      proc.Signal(syscall.SIGTERM)
    } else {
      // find old process, send SIGHUP then exit self
      // the 'real' new process running later starts by old process received SIGHUP
      proc.Signal(syscall.SIGHUP)
    }
    return
  }
  
  // handle start(default) command
  if err == nil {
    err = errors.New("daemon already started")
    return
  }
  
  err = initListeners(ctx.Servers, cl)
  if err != nil {
    return
  }
  
  err = reload()
  if err != nil {
    return
  }
    
  return 
}


