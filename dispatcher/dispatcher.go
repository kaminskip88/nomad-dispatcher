package dispatcher

import (
	"fmt"
	"time"

	"github.com/hashicorp/nomad/api"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

type Dispatcher struct {
	client     *api.Client
	job        string
	meta       map[string]string
	payload    []byte
	interval   time.Duration
	jobRetry   int
	allocRetry int
}

func NewDispatcher(c *Config) (*Dispatcher, error) {
	// logging config
	switch {
	case c.LogError:
		log.SetLevel(log.ErrorLevel)
	case c.LogDebug:
		log.SetLevel(log.DebugLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	// set global interval
	interval, err := time.ParseDuration(c.Interval)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}

	// Default config
	conf := api.DefaultConfig()
	if v := c.Addr; v != "" {
		conf.Address = v
	}
	if v := c.Region; v != "" {
		conf.Region = v
	}
	// if v := c.Token; v != "" {
	// 	conf.SecretID = v
	// }

	// Nomad API client
	client, err := api.NewClient(conf)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}

	dispatcher := &Dispatcher{
		client:     client,
		job:        c.Job,
		meta:       c.Meta,
		payload:    []byte(c.Payload),
		interval:   interval,
		jobRetry:   c.JobRetry,
		allocRetry: c.AllocRetry,
	}
	return dispatcher, nil
}

type Config struct {
	Job        string
	Region     string
	Addr       string
	LogDebug   bool
	LogError   bool
	Meta       map[string]string
	Payload    string
	Interval   string
	AllocRetry int
	JobRetry   int
	// Token      string            `arg:"env:NOMAD_TOKEN" help:"Nomad ACL token"`
}

func (d Dispatcher) Dispatch() error {

	jr, _, err := d.client.Jobs().Dispatch(
		d.job, d.meta, d.payload, &api.WriteOptions{},
	)
	if err != nil {
		log.Error(err.Error())
		return err
	}
	log.WithFields(log.Fields{
		"Eval": jr.EvalID,
	}).Info("Starting job evaluation")
	eval, _, err := d.client.Evaluations().Info(jr.EvalID, &api.QueryOptions{})
	if err != nil {
		log.Error(err.Error())
		return err
	}

	var g errgroup.Group

	g.Go(func() error {
		err := d.monitorJob(d.client, eval.JobID, &g)
		return err
	})
	err = g.Wait()
	if err != nil {
		log.Error(err.Error())
		return err
	}
	log.Infof("Finished successfully")
	return nil
}

type state struct {
	State  string
	Events int
}

func (d Dispatcher) monitorJob(c *api.Client, ID string, g *errgroup.Group) error {
	var retry int
	var knownAlloc []string
	saveState := "none"
job:
	for {
		var errs []error
		job, _, err := c.Jobs().Info(ID, &api.QueryOptions{})
		errs = append(errs, err)
		allocs, _, err := c.Jobs().Allocations(ID, false, &api.QueryOptions{})
		errs = append(errs, err)
		for _, i := range errs {
			if i != nil {
				log.Error(i)
				if retry < d.jobRetry {
					retry++
					time.Sleep(d.interval)
					continue job
				} else {
					return fmt.Errorf("cannot get job data in %d retries", d.jobRetry)
				}
			}
		}
		retry = 0
		// log Job state
		if saveState != *job.Status {
			log.WithField("job", ID).
				Infof("Job state: %s => %s", saveState, *job.Status)
			saveState = *job.Status
		}
		// check if job finished
		if *job.Status == "dead" {
			return nil
		}
		// launch allocation checks
		for _, alloc := range allocs {
			if !find(knownAlloc, alloc.ID) {
				alloc := alloc // https://golang.org/doc/faq#closures_and_goroutines
				g.Go(func() error {
					logger := log.WithField("alloc", alloc.ID)
					err := d.monitorAlloc(c, logger, alloc.ID, g)
					return err
				})
				knownAlloc = append(knownAlloc, alloc.ID)
			}
		}
		time.Sleep(d.interval)
	}
}

func (d Dispatcher) monitorAlloc(c *api.Client, log *log.Entry, allocID string, g *errgroup.Group) error {
	var retry int
	// create/close cancel chanel for log streams
	cancel := make(chan struct{})
	defer close(cancel)

	ts := make(map[string]state)
	for {
		a, _, err := c.Allocations().Info(allocID, &api.QueryOptions{})
		if err != nil {
			log.Error(err)
			if retry < d.allocRetry {
				retry++
			} else {
				return fmt.Errorf("cannot get allocation info in %d retries", d.allocRetry)
			}
		} else {
			retry = 0

			switch a.ClientStatus {
			case api.AllocClientStatusComplete:
				log.Info("allocation completed")
				return nil
			case api.AllocClientStatusFailed, api.AllocClientStatusLost:
				log.Warnf("allocation %s", a.ClientStatus)
				done, left := a.RescheduleInfo(time.Time{})
				if left > done {
					log.Infof("reschedule attempt %d of %d", done, left)
					return nil
				} else {
					return fmt.Errorf("no reschedule attempts left")
				}
			}
			for taskN, task := range a.TaskStates {
				cts, ok := ts[taskN]
				// set initial state for new task
				if !ok {
					cts.State = "none"
				}
				// log state
				if cts.State != task.State {
					log.Debugf("Task state: %s => %s", cts.State, task.State)

					// start logging when first swithced to running state
					if task.State == "running" {
						stdout, stdoutErr := c.AllocFS().Logs(a, true, taskN, "stdout", "start", 0, cancel, &api.QueryOptions{})
						g.Go(func() error {
							monitorLog("STDOUT:", stdout, stdoutErr, cancel, log)
							return nil
						})
						stderr, strerrErr := c.AllocFS().Logs(a, true, taskN, "stderr", "start", 0, cancel, &api.QueryOptions{})
						g.Go(func() error {
							monitorLog("STDERR:", stderr, strerrErr, cancel, log)
							return nil
						})
					}
				}
				cts.State = task.State

				// log events
				for i, e := range task.Events {
					if i >= cts.Events {
						log.Debug(e.Type + ":")
						log.Info(e.DisplayMessage)
					}
				}
				cts.Events = len(task.Events)
				ts[taskN] = cts
			}
		}
		time.Sleep(d.interval)
	}
}

func monitorLog(label string, c <-chan *api.StreamFrame, cErr <-chan error, cancel <-chan struct{}, log *log.Entry) {
	for {
		select {
		case l := <-c:
			fmt.Print(label + "" + string(l.Data))
		case err := <-cErr:
			log.Warn(err.Error())
		case _, ok := <-cancel:
			if !ok {
				log.Debug("stop log stream")
				return
			}
		}
	}
}

func find(source []string, value string) bool {
	for _, item := range source {
		if item == value {
			return true
		}
	}
	return false
}
