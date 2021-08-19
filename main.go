package main

import (
	"os"

	"github.com/alexflint/go-arg"
	"github.com/kaminskip88/nomad-dispatcher/dispatcher"
)

var conf struct {
	Job        string            `arg:"positional,required" help:"Nomad Job ID"`
	Region     string            `help:"Nomad region"`
	Addr       string            `help:"Nomad Address"`
	LogDebug   bool              `arg:"-v" help:"Debug output"`
	LogError   bool              `arg:"-q" help:"Show only critical messages"`
	Meta       map[string]string `arg:"-m,separate" help:"Job metadata"`
	Payload    string            `arg:"-p" help:"Path to payload file"`
	Interval   string            `default:"2s" help:"API poll intervals"`
	AllocRetry int               `default:"3" help:"Number of retries to recieve allocation info"`
	JobRetry   int               `default:"3" help:"Number of retries to recieve job info"`
}

func main() {
	arg.MustParse(&conf)
	// var c dispatcher.Config
	c := dispatcher.Config(conf)
	d, err := dispatcher.NewDispatcher(&c)
	if err != nil {
		os.Exit(1)
	}
	err = d.Dispatch()
	if err != nil {
		os.Exit(1)
	}
}
