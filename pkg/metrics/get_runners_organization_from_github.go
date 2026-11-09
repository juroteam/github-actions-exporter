package metrics

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/spendesk/github-actions-exporter/pkg/config"

	"github.com/google/go-github/v45/github"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	runnersOrganizationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_organization_status",
			Help: "runner status",
		},
		[]string{"organization", "os", "name", "id", "busy", "labels"},
	)
)

func getAllOrgRunners(orga string) []*github.Runner {
	var runners []*github.Runner
	opt := &github.ListOptions{PerPage: 200}

	for {
		resp, rr, err := client.Actions.ListOrganizationRunners(context.Background(), orga, opt)
		if rl_err, ok := err.(*github.RateLimitError); ok {
			log.Printf("ListOrganizationRunners ratelimited. Pausing until %s", rl_err.Rate.Reset.Time.String())
			time.Sleep(time.Until(rl_err.Rate.Reset.Time))
			continue
		} else if err != nil {
			log.Printf("ListOrganizationRunners error for org %s: %s", orga, err.Error())
			return runners
		}

		runners = append(runners, resp.Runners...)
		if rr.NextPage == 0 {
			break
		}
		opt.Page = rr.NextPage
	}
	return runners
}

// customRunnerLabels - comma-joined custom labels of a runner (default labels like self-hosted, Linux, X64 are skipped)
func customRunnerLabels(runner *github.Runner) string {
	var labels []string
	for _, l := range runner.Labels {
		if l.GetType() == "custom" {
			labels = append(labels, l.GetName())
		}
	}
	return strings.Join(labels, ",")
}

// getRunnersOrganizationFromGithub - return information about runners and their status for an organization
func getRunnersOrganizationFromGithub() {
	for {
		for _, orga := range config.Github.Organizations.Value() {
			runners := getAllOrgRunners(orga)
			for _, runner := range runners {
				var online float64 = 0
				if runner.GetStatus() == "online" {
					online = 1
				}
				runnersOrganizationGauge.WithLabelValues(orga, *runner.OS, *runner.Name, strconv.FormatInt(runner.GetID(), 10), strconv.FormatBool(runner.GetBusy()), customRunnerLabels(runner)).Set(online)
			}
		}

		time.Sleep(time.Duration(config.Github.Refresh) * time.Second)
		runnersOrganizationGauge.Reset()
	}
}
