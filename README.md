`gozd`, is a configurable zero downtime daemon(TCP/HTTP/FCGI) framework write in golang. All it takes is integrating just one simple call to gozd.Daemonize(). Then you will get:

1. upgrade binary/service with absolutely zero downtime. high availability!
2. listen to multiple port and/or socket in same program
3. gracefully shutdown service without break and existing connections

##How to install

    go get -u bitbucket.org/PinIdea/go-zero-downtime-daemon

##Sample Code & Integration

There are sample programs in the "examples" directory.

Basic intergration steps are:

1. Initialize a channel and perpare a goroutine to handler new net.Listener 
2. Call `gozd.Daemonize(Context, chan net.Listener)` to initialize `gozd` & obtain a channel to receive exit signal from `gozd`.
3. Wait till daemon send a exit signal, do some cleanup if you want.

##Daemon Usage

> kill -TERM <pid>  send signal to gracefully shutdown daemon without break existing connections and services.

> kill -HUP <pid>  send signal to restart daemon's latest binary, without break existing connections and services.

##Daemon Configuration

    ctx  := gozd.Context{
      Hash:[DAEMON_NAME],
      Command:[start,stop,reload],
      Logfile:[LOG_FILEPATH,""], 
      Maxfds: [RLIMIT_NOFILE],
      User:   [USERID],
      Group:  [GROUPID],
      Directives:map[string]gozd.Server{
        [SERVER_ID]:gozd.Server{
          Network:["unix","tcp"],
          Address:[SOCKET_FILE(eg./tmp/daemon.sock),TCP_ADDR(eg. 127.0.0.1:80)],
          Chmod:0666,
        },
        ...
      },
    }
  
##TODO

1. more examples to cover usage of:
    + config file
    + command arguments
    + fcgi server
    + http server
    + https server
2. test cases
    + test config change while HUP
    + race condition test
    + stress test

##How to contribute

Help is needed to write more test cases and stress test.

Patches or suggestions that can make the code simpler or easier to use are welcome to submit to [issue area|https://bitbucket.org/PinIdea/go-zero-downtime-daemon/issues?status=new&status=open].

##How it works

The basic principle: master process fork a process, and child process evecve corresponding binary. 

`os.StartProcess` did the trick to append files that contains handle that is can be inherited. Then the child process can start listening from same handle which we passed fd number via environment variable as index. After that we use `net.FileListener` to recreate net.Listener interface to gain accesss to the socket created by last master process.

We also expand the net.Listener and net.Conn, so that the master process will stop accept new connection and wait untill all existing connection to dead naturely before exit the process. 

The detail in in the code of reload() in daemon.go. 

## Special Thanks

The hotupdate idea and code is inspaired by nginx and beego. Thanks.