package pool

import (
	"sync"
	"time"

	"github.com/hauxe/gom/environment"
	lib "github.com/hauxe/gom/library"
	sdklog "github.com/hauxe/gom/log"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

const (
	maxWorkers = 1000
)

// WorkerConfig defines pool properties
type WorkerConfig struct {
	MaxWorkers int `env:"POOL_MAX_WORKERS"`
}

// Worker represents the worker that executes the job
type Worker struct {
	Config     *WorkerConfig
	Logger     sdklog.Factory
	WorkerPool chan chan Job
	JobChannel chan Job
	quit       chan struct{}
	isStopped  bool
	mux        sync.RWMutex
}

// CreateWorker create a worker pool
func CreateWorker(options ...func(*environment.ENVConfig) error) (worker *Worker, err error) {
	env, err := environment.CreateENV(options...)
	if err != nil {
		return nil, errors.Wrap(err, lib.StringTags("create server", "create env"))
	}
	config := WorkerConfig{maxWorkers}
	if err = env.Parse(&config); err != nil {
		return nil, errors.Wrap(err, lib.StringTags("create worker", "parse env"))
	}
	logger, err := zap.NewDevelopment()
	if err != nil {
		return nil, errors.Wrap(err, lib.StringTags("create worker", "get logger"))
	}

	return &Worker{
		Config:     &config,
		WorkerPool: make(chan chan Job, config.MaxWorkers),
		JobChannel: make(chan Job),
		quit:       make(chan struct{}),
		Logger:     sdklog.Factory{Logger: logger},
	}, nil
}

// StartServer method starts the run loop for the worker, listening for a quit channel in
// case we need to stop it
func (w *Worker) StartServer(options ...func() error) (err error) {
	if w.Config == nil {
		return errors.New(lib.StringTags("start worker", "config not found"))
	}
	if err = lib.RunOptionalFunc(options...); err != nil {
		return errors.Wrap(err, lib.StringTags("start worker", "option error"))
	}
	var wg sync.WaitGroup
	for i := 0; i < w.Config.MaxWorkers; i++ {
		wg.Add(1)
		go func() {
			wg.Done()
			for {
				// register the current worker into the worker queue.
				w.WorkerPool <- w.JobChannel

				select {
				case job := <-w.JobChannel:
					// we have received a work request.
					if err := job.Execute(); err != nil {
						w.ErrorLog(job, err)
					}

				case <-w.quit:
					// we have received a signal to stop
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		wg.Done()
		<-w.quit
		// update status and close all channels
		w.mux.Lock()
		defer w.mux.Unlock()
		w.isStopped = true
		close(w.WorkerPool)
		close(w.JobChannel)
		return
	}()
	wg.Wait()
	return nil
}

// StopServer signals the worker to stop listening for work requests.
func (w *Worker) StopServer() {
	close(w.quit)
}

// QueueJob queue a job with timeout
func (w *Worker) QueueJob(job Job, timeout time.Duration) (err error) {
	w.mux.RLock()
	if w.isStopped {
		err = errors.New("send job to a stopped worker")
		w.ErrorLog(job, err)
		w.mux.RUnlock()
		return
	}
	w.mux.RUnlock()
	var t <-chan time.Time
	if timeout > 0 {
		t = time.After(timeout)
	}
	select {
	case jobChannel, ok := <-w.WorkerPool:
		if ok {
			jobChannel <- job
		} else {
			err = errors.Errorf("worker channel is closed unexpectedly", timeout)
		}
	case <-t:
		err = errors.Errorf("wait for worker timedout after %d", timeout)
		w.ErrorLog(job, err)
	}
	return
}

// ErrorLog log error
func (w *Worker) ErrorLog(job Job, err error) {
	// log error
	logger := w.Logger.Bg()
	if ctx := job.GetContext(); ctx != nil {
		logger = w.Logger.For(ctx)
	}
	logger.Error("worker job error",
		zap.String("job name", job.Name()),
		zap.Error(err))
}

// SetMaxWorkersOption set max worker
func (w *Worker) SetMaxWorkersOption(maxWorkers int) func() error {
	return func() (err error) {
		w.Config.MaxWorkers = maxWorkers
		w.WorkerPool = make(chan chan Job, maxWorkers)
		return nil
	}
}
