//! Scheduled jobs (port of `yekonga/cronjob.go`). Jobs run on the server's
//! own schedule when `hasCronjob` is set: either every fixed interval, or at
//! a recurring time of day/week/month.

use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Datelike, Timelike, Utc};

use crate::app::{BoxFuture, Yekonga};

/// A job's callback: given the app and the tick time.
pub type CronCallback = Arc<dyn Fn(Yekonga, DateTime<Utc>) -> BoxFuture<()> + Send + Sync>;

/// How often a timed job repeats (Go's `JobFrequency`).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum JobFrequency {
    Hourly,
    Daily,
    Weekly,
    Monthly,
}

#[derive(Clone)]
enum Schedule {
    /// Every `delay`.
    Interval { delay: Duration, elapsed: Duration },
    /// At a time of day/week/month.
    At {
        frequency: JobFrequency,
        run_time: DateTime<Utc>,
        last_run_key: Option<String>,
    },
}

/// A registered job.
#[derive(Clone)]
pub struct CronJob {
    #[allow(dead_code)]
    name: String,
    schedule: Schedule,
    callback: CronCallback,
    running: Arc<std::sync::atomic::AtomicBool>,
}

impl Yekonga {
    /// Runs `callback` every `interval` (Go's `RegisterCronjob`). Jobs only
    /// run when `hasCronjob` is set and [`start`](Yekonga::start) is called.
    pub fn register_cronjob(
        &self,
        name: &str,
        interval: Duration,
        callback: impl Fn(Yekonga, DateTime<Utc>) -> BoxFuture<()> + Send + Sync + 'static,
    ) {
        self.cron_jobs()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .push(CronJob {
                name: name.to_string(),
                schedule: Schedule::Interval {
                    delay: interval,
                    elapsed: Duration::ZERO,
                },
                callback: Arc::new(callback),
                running: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            });
    }

    /// Runs `callback` at `run_time` on the given frequency (Go's
    /// `RegisterCronjobAt`): the time of day for daily, the weekday and time
    /// for weekly, the day of month and time for monthly, the minute for
    /// hourly.
    pub fn register_cronjob_at(
        &self,
        name: &str,
        frequency: JobFrequency,
        run_time: DateTime<Utc>,
        callback: impl Fn(Yekonga, DateTime<Utc>) -> BoxFuture<()> + Send + Sync + 'static,
    ) {
        self.cron_jobs()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .push(CronJob {
                name: name.to_string(),
                schedule: Schedule::At {
                    frequency,
                    run_time,
                    last_run_key: None,
                },
                callback: Arc::new(callback),
                running: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            });
    }
}

/// The recurring part of a time, at the given frequency, as a comparable key
/// (Go compares `time.Format` strings).
fn frequency_key(frequency: JobFrequency, at: DateTime<Utc>) -> String {
    match frequency {
        JobFrequency::Hourly => format!("{:02}", at.minute()),
        JobFrequency::Daily => format!("{:02}:{:02}", at.hour(), at.minute()),
        JobFrequency::Weekly => format!(
            "{} {:02}:{:02}",
            at.weekday().number_from_monday(),
            at.hour(),
            at.minute()
        ),
        JobFrequency::Monthly => format!("{:02}T{:02}:{:02}", at.day(), at.hour(), at.minute()),
    }
}

/// Starts the scheduler: a task that ticks every second and runs due jobs
/// (Go's `Cronjob.initialize`). Each run is spawned so a slow job doesn't
/// hold up the others, and a job never overlaps itself.
pub(crate) fn start(app: Yekonga) {
    tokio::spawn(async move {
        let mut ticker = tokio::time::interval(Duration::from_secs(1));
        ticker.tick().await; // the first tick is immediate
        loop {
            ticker.tick().await;
            let now = Utc::now();
            let mut jobs = app.cron_jobs().write().unwrap_or_else(|e| e.into_inner());
            for job in jobs.iter_mut() {
                let due = match &mut job.schedule {
                    Schedule::Interval { delay, elapsed } => {
                        *elapsed += Duration::from_secs(1);
                        if *elapsed >= *delay {
                            *elapsed = Duration::ZERO;
                            true
                        } else {
                            false
                        }
                    }
                    Schedule::At {
                        frequency,
                        run_time,
                        last_run_key,
                    } => {
                        let key = frequency_key(*frequency, now);
                        let due = frequency_key(*frequency, *run_time) == key
                            && last_run_key.as_deref() != Some(key.as_str());
                        if due {
                            *last_run_key = Some(key);
                        }
                        due
                    }
                };

                if due
                    && job
                        .running
                        .compare_exchange(
                            false,
                            true,
                            std::sync::atomic::Ordering::SeqCst,
                            std::sync::atomic::Ordering::SeqCst,
                        )
                        .is_ok()
                {
                    let (callback, running, app) =
                        (job.callback.clone(), job.running.clone(), app.clone());
                    tokio::spawn(async move {
                        callback(app, now).await;
                        running.store(false, std::sync::atomic::Ordering::SeqCst);
                    });
                }
            }
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;

    #[test]
    fn frequency_keys_match_the_recurring_part() {
        // 2026-09-28 is a Monday.
        let at = Utc.with_ymd_and_hms(2026, 9, 28, 14, 7, 30).unwrap();
        assert_eq!(frequency_key(JobFrequency::Hourly, at), "07");
        assert_eq!(frequency_key(JobFrequency::Daily, at), "14:07");
        assert_eq!(frequency_key(JobFrequency::Weekly, at), "1 14:07");
        assert_eq!(frequency_key(JobFrequency::Monthly, at), "28T14:07");

        // Same minute next hour: hourly matches, daily doesn't.
        let later = Utc.with_ymd_and_hms(2026, 9, 28, 15, 7, 0).unwrap();
        assert_eq!(
            frequency_key(JobFrequency::Hourly, later),
            frequency_key(JobFrequency::Hourly, at)
        );
        assert_ne!(
            frequency_key(JobFrequency::Daily, later),
            frequency_key(JobFrequency::Daily, at)
        );
    }
}
