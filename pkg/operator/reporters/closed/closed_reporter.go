package closed

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/eparis/bugzilla"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/mfojtik/bugzilla-operator/pkg/cache"
	"github.com/mfojtik/bugzilla-operator/pkg/operator/bugutil"
	"github.com/mfojtik/bugzilla-operator/pkg/operator/config"
	"github.com/mfojtik/bugzilla-operator/pkg/slack"
)

type BlockersReporter struct {
	config            config.OperatorConfig
	newBugzillaClient func() cache.BugzillaClient
	slackClient       slack.ChannelClient
}

func NewClosedReporter(operatorConfig config.OperatorConfig, scheduleInformer factory.Informer, newBugzillaClient func() cache.BugzillaClient, slackClient slack.ChannelClient, recorder events.Recorder) factory.Controller {
	c := &BlockersReporter{
		config:            operatorConfig,
		newBugzillaClient: newBugzillaClient,
		slackClient:       slackClient,
	}
	return factory.New().WithSync(c.sync).WithInformers(scheduleInformer).ToController("BlockersReporter", recorder)
}

func (c *BlockersReporter) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	client := c.newBugzillaClient()
	report, err := Report(ctx, client, syncCtx.Recorder(), &c.config)
	if err != nil {
		return err
	}
	if len(report) == 0 {
		return nil
	}

	if err := c.slackClient.MessageChannel(report); err != nil {
		syncCtx.Recorder().Warningf("DeliveryFailed", "Failed to deliver closed bug counts: %v", err)
		return err
	}

	return nil
}

func Report(ctx context.Context, client cache.BugzillaClient, recorder events.Recorder, config *config.OperatorConfig) (string, error) {
	closedBugs, err := getClosedList(client, config)
	if err != nil {
		recorder.Warningf("BugSearchFailed", err.Error())
		return "", err
	}

	resolutionMap := map[string][]bugzilla.Bug{}
	for _, bug := range closedBugs {
		resolutionMap[bug.Resolution] = append(resolutionMap[bug.Resolution], *bug)
	}

	messageMap := map[string][]string{}
	resolutions := sets.NewString()
	for resolution, bugs := range resolutionMap {
		ids := []string{}
		for _, b := range bugs {
			ids = append(ids, fmt.Sprintf("<https://bugzilla.redhat.com/show_bug.cgi?id=%d|#%d>", b.ID, b.ID))
		}
		if messageMap[resolution] == nil {
			messageMap[resolution] = []string{}
		}
		messageMap[resolution] = append(messageMap[resolution], fmt.Sprintf("> %s closed as _%s_ (%s)", bugutil.BugCountPlural(len(bugs), false), resolution, strings.Join(ids, ",")))
		if !resolutions.Has(resolution) {
			resolutions.Insert(resolution)
		}
	}

	sortedResolutions := resolutions.List()
	sort.Strings(sortedResolutions)
	var messages []string
	for _, resolution := range sortedResolutions {
		messages = append(messages, messageMap[resolution]...)
	}

	if len(closedBugs) == 0 {
		return "*No bugs closed in last 24h* :-(\n", nil
	}

	report := fmt.Sprintf("*%s Closed in the last 24h*:\n%s\n", bugutil.BugCountPlural(len(closedBugs), true), strings.Join(messages, "\n"))
	return report, nil
}

func getClosedList(client cache.BugzillaClient, config *config.OperatorConfig) ([]*bugzilla.Bug, error) {
	return client.Search(bugzilla.Query{
		Classification: []string{"Red Hat"},
		Product:        []string{"OpenShift Container Platform"},
		Status:         []string{"CLOSED"},
		Component:      config.Components,
		Advanced: []bugzilla.AdvancedQuery{
			{
				Field: "bug_status",
				Op:    "changedafter",
				Value: "-1d",
			},
		},
		IncludeFields: []string{
			"assigned_to",
			"keywords",
			"status",
			"resolution",
			"severity",
			"priority",
			"target_release",
			"cf_devel_whiteboard",
		},
	})
}
