/*
Copyright 2022 The KodeRover Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"regexp"
	"strconv"

	"github.com/google/go-github/v35/github"
	"github.com/hashicorp/go-multierror"
	"go.uber.org/zap"

	"github.com/koderover/zadig/v2/pkg/microservice/aslan/config"
	commonmodels "github.com/koderover/zadig/v2/pkg/microservice/aslan/core/common/repository/models"
	commonrepo "github.com/koderover/zadig/v2/pkg/microservice/aslan/core/common/repository/mongodb"
	scanningservice "github.com/koderover/zadig/v2/pkg/microservice/aslan/core/workflow/testing/service"
)

type gitEventMatcherForScanning interface {
	Match(repository *commonmodels.ScanningHook) (bool, error)
}

func TriggerScanningByGithubEvent(event interface{}, requestID string, log *zap.SugaredLogger) error {
	//1.find configured testing
	scanningList, _, err := commonrepo.NewScanningColl().List(nil, 0, 0)
	if err != nil {
		log.Errorf("failed to list scanning %v", err)
		return err
	}

	mErr := &multierror.Error{}
	diffSrv := func(pullRequestEvent *github.PullRequestEvent, codehostId int) ([]string, error) {
		return findChangedFilesOfPullRequest(pullRequestEvent, codehostId)
	}

	log.Infof("Matching scanning list to find matched task to run.")
	for _, scanning := range scanningList {
		if scanning.AdvancedSetting.HookCtl != nil && scanning.AdvancedSetting.HookCtl.Enabled {
			for _, item := range scanning.AdvancedSetting.HookCtl.Items {
				matcher := createGithubEventMatcherForScanning(event, diffSrv, scanning, log)
				if matcher == nil {
					log.Infof("got a nil matcher for trigger: %s/%s, stopping...", item.RepoOwner, item.RepoName)
					continue
				}
				if matches, err := matcher.Match(item); err != nil {
					mErr = multierror.Append(err)
				} else if matches {
					log.Infof("event match hook %v of %s", item, scanning.Name)
					var mergeRequestID string
					if ev, isPr := event.(*github.PullRequestEvent); isPr {
						mergeRequestID = strconv.Itoa(*ev.PullRequest.Number)
					}
					triggerRepoInfo := make([]*scanningservice.ScanningRepoInfo, 0)
					for _, scanningRepo := range scanning.Repos {
						// if this is the triggering repo, we simply skip it and add it later with correct info
						if scanningRepo.CodehostID == item.CodehostID && scanningRepo.RepoOwner == item.RepoOwner && scanningRepo.RepoName == item.RepoName {
							continue
						}
						triggerRepoInfo = append(triggerRepoInfo, &scanningservice.ScanningRepoInfo{
							CodehostID: scanningRepo.CodehostID,
							Source:     scanningRepo.Source,
							RepoOwner:  scanningRepo.RepoOwner,
							RepoName:   scanningRepo.RepoName,
							Branch:     scanningRepo.Branch,
						})
					}

					repoInfo := &scanningservice.ScanningRepoInfo{
						CodehostID: item.CodehostID,
						Source:     item.Source,
						RepoOwner:  item.RepoOwner,
						RepoName:   item.RepoName,
						Branch:     item.Branch,
					}
					if mergeRequestID != "" {
						prID, err := strconv.Atoi(mergeRequestID)
						if err != nil {
							log.Errorf("failed to convert mergeRequestID: %s to int, error: %s", mergeRequestID, err)
							mErr = multierror.Append(mErr, err)
							continue
						}
						repoInfo.PR = prID
					}

					triggerRepoInfo = append(triggerRepoInfo, repoInfo)

					if resp, err := scanningservice.CreateScanningTaskV2(scanning.ID.Hex(), "webhook", "", "", triggerRepoInfo, "", log); err != nil {
						log.Errorf("failed to create testing task when receive event %v due to %v ", event, err)
						mErr = multierror.Append(mErr, err)
					} else {
						log.Infof("succeed to create task %v", resp)
					}
				} else {
					log.Debugf("event not matches %v", item)
				}
			}
		}
	}
	return mErr.ErrorOrNil()
}

type githubPushEventMatcherForScanning struct {
	log      *zap.SugaredLogger
	scanning *commonmodels.Scanning
	event    *github.PushEvent
}

func (gpem githubPushEventMatcherForScanning) Match(hookRepo *commonmodels.ScanningHook) (bool, error) {
	ev := gpem.event
	if hookRepo == nil {
		return false, nil
	}
	if (hookRepo.RepoOwner + "/" + hookRepo.RepoName) == *ev.Repo.FullName {
		matchRepo := ConvertScanningHookToMainHookRepo(hookRepo)

		if !EventConfigured(matchRepo, config.HookEventPush) {
			return false, nil
		}

		if hookRepo.Branch != getBranchFromRef(*ev.Ref) {
			return false, nil
		}

		hookRepo.Branch = getBranchFromRef(*ev.Ref)
		var changedFiles []string
		for _, commit := range ev.Commits {
			changedFiles = append(changedFiles, commit.Added...)
			changedFiles = append(changedFiles, commit.Removed...)
			changedFiles = append(changedFiles, commit.Modified...)
		}

		return MatchChanges(matchRepo, changedFiles), nil
	}

	return false, nil
}

type githubMergeEventMatcherForScanning struct {
	diffFunc githubPullRequestDiffFunc
	log      *zap.SugaredLogger
	scanning *commonmodels.Scanning
	event    *github.PullRequestEvent
}

func (gmem githubMergeEventMatcherForScanning) Match(hookRepo *commonmodels.ScanningHook) (bool, error) {
	ev := gmem.event
	if hookRepo == nil {
		return false, nil
	}
	// TODO: match codehost
	if (hookRepo.RepoOwner + "/" + hookRepo.RepoName) == *ev.PullRequest.Base.Repo.FullName {
		matchRepo := ConvertScanningHookToMainHookRepo(hookRepo)
		if !EventConfigured(matchRepo, config.HookEventPr) {
			return false, nil
		}

		hookRepo.Branch = *ev.PullRequest.Base.Ref

		isRegExp := matchRepo.IsRegular

		if !isRegExp {
			if *ev.PullRequest.Base.Ref != hookRepo.Branch {
				return false, nil
			}
		} else {
			if matched, _ := regexp.MatchString(hookRepo.Branch, *ev.PullRequest.Base.Ref); !matched {
				return false, nil
			}
		}

		if *ev.PullRequest.State == "open" {
			var changedFiles []string
			changedFiles, err := gmem.diffFunc(ev, hookRepo.CodehostID)
			if err != nil {
				gmem.log.Warnf("failed to get changes of event %v", ev)
				return false, err
			}
			gmem.log.Debugf("succeed to get %d changes in merge event", len(changedFiles))

			return MatchChanges(matchRepo, changedFiles), nil
		}

	}
	return false, nil
}

type githubTagEventMatcherForScanning struct {
	log      *zap.SugaredLogger
	scanning *commonmodels.Scanning
	event    *github.CreateEvent
}

func (gtem githubTagEventMatcherForScanning) Match(hookRepo *commonmodels.ScanningHook) (bool, error) {
	ev := gtem.event
	if (hookRepo.RepoOwner + "/" + hookRepo.RepoName) == *ev.Repo.FullName {
		hookInfo := ConvertScanningHookToMainHookRepo(hookRepo)
		if !EventConfigured(hookInfo, config.HookEventTag) {
			return false, nil
		}

		hookRepo.Branch = *ev.Repo.DefaultBranch

		return true, nil
	}

	return false, nil
}

func createGithubEventMatcherForScanning(
	event interface{}, diffSrv githubPullRequestDiffFunc, scanning *commonmodels.Scanning, log *zap.SugaredLogger,
) gitEventMatcherForScanning {
	switch evt := event.(type) {
	case *github.PushEvent:
		return &githubPushEventMatcherForScanning{
			scanning: scanning,
			log:      log,
			event:    evt,
		}
	case *github.PullRequestEvent:
		return &githubMergeEventMatcherForScanning{
			diffFunc: diffSrv,
			log:      log,
			event:    evt,
			scanning: scanning,
		}
	case *github.CreateEvent:
		return &githubTagEventMatcherForScanning{
			scanning: scanning,
			log:      log,
			event:    evt,
		}
	}

	return nil
}
