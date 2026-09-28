package yekonga

import (
	"time"
)

func NewCronjob(app *YekongaData) *Cronjob {
	c := Cronjob{
		app:  app,
		jobs: make([]Job, 0),
	}

	c.initialize()

	return &c
}

type jobType string

const (
	interval jobType = "interval"
	specific jobType = "specific"
)

type JobFrequency string

const (
	HOURLY  JobFrequency = "HOURLY"
	DAILY   JobFrequency = "DAILY"
	WEEKLY  JobFrequency = "WEEKLY"
	MONTHLY JobFrequency = "MONTHLY"
)

type Job struct {
	name      string
	delay     time.Duration
	time      time.Duration
	runTime   time.Time
	callback  func(app *YekongaData, time time.Time)
	process   bool
	lastRun   time.Time
	lastCheck time.Time
	kind      jobType
	frequency JobFrequency
}

type Cronjob struct {
	app  *YekongaData
	jobs []Job
}

func (c *Cronjob) initialize() {

	if c.app.Config.HasCronjob {
		go func() {
			ticker := time.NewTicker(time.Second * 1)
			defer ticker.Stop()

			for {
				t := <-ticker.C
				// console.Error("Count", t.String())
				for i, job := range c.jobs {
					c.jobs[i].time += time.Duration(1) * time.Second

					if job.kind == interval {
						c.jobs[i].lastCheck = t

						if job.time >= job.delay && !job.process {
							c.jobs[i].lastRun = t
							c.jobs[i].time = time.Duration(0)
							c.jobs[i].process = true

							go func() {
								job.callback(c.app, t)
								c.jobs[i].process = false
							}()
						}
					} else {
						runNow := false
						processed := false

						switch job.frequency {
						case HOURLY:
							format := "04"
							runNow = (job.runTime.Format(format) == t.Format(format))
							processed = (job.lastCheck.Format(format) == t.Format(format))
						case DAILY:
							format := time.Kitchen
							runNow = (job.runTime.Format(format) == t.Format(format))
							processed = (job.lastCheck.Format(format) == t.Format(format))
						case WEEKLY:
							format := "Monday 15:04"
							runNow = (job.runTime.Format(format) == t.Format(format))
							processed = (job.lastCheck.Format(format) == t.Format(format))
						case MONTHLY:
							format := "02T15:04"
							runNow = (job.runTime.Format(format) == t.Format(format))
							processed = (job.lastCheck.Format(format) == t.Format(format))
						}

						if runNow && !job.process && !processed {
							c.jobs[i].lastRun = t
							c.jobs[i].time = time.Duration(0)
							c.jobs[i].process = true

							go func() {
								job.callback(c.app, t)
								c.jobs[i].process = false
							}()
						}

						c.jobs[i].lastCheck = t
					}

					// console.Info("Info", job.name, job.delay, job.time)
				}
			}
		}()
	}
}

func (c *Cronjob) registerJob(name string, delay time.Duration, callback func(app *YekongaData, time time.Time)) {
	c.jobs = append(c.jobs, Job{
		name:     name,
		delay:    delay,
		time:     0,
		callback: callback,
		process:  false,
		kind:     interval,
	})
}

func (c *Cronjob) registerJobAt(name string, frequency JobFrequency, time time.Time, callback func(app *YekongaData, time time.Time)) {
	c.jobs = append(c.jobs, Job{
		name:      name,
		runTime:   time,
		time:      0,
		callback:  callback,
		process:   false,
		kind:      specific,
		frequency: frequency,
	})
}
