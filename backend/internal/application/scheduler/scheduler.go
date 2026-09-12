// Package scheduler 进程内任务调度（T024，research R16）。
// robfig/cron + 有界并发（探测并发上限 32，R18）；启动恢复钩子在 serve 时调用。
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron *cron.Cron
	sem  chan struct{} // 探测并发闸（≤32）
}

func New(probeConcurrency int) *Scheduler {
	if probeConcurrency <= 0 {
		probeConcurrency = 32
	}
	return &Scheduler{
		cron: cron.New(cron.WithSeconds()),
		sem:  make(chan struct{}, probeConcurrency),
	}
}

// Every 注册固定周期任务（去重抖动：同刻只跑一轮）。
func (s *Scheduler) Every(name string, interval time.Duration, job func(ctx context.Context)) {
	s.cron.AddFunc("@every "+interval.String(), func() {
		s.sem <- struct{}{}
		defer func() { <-s.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), interval-100*time.Millisecond)
		defer cancel()
		start := time.Now()
		job(ctx)
		if d := time.Since(start); d > interval {
			slog.Warn("任务超周期", "job", name, "took", d.String())
		}
	})
	slog.Info("调度任务注册", "job", name, "interval", interval.String())
}

func (s *Scheduler) Start() { s.cron.Start() }

func (s *Scheduler) Stop(ctx context.Context) {
	<-s.cron.Stop().Done()
}
