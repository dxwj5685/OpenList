package edgecos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/go-resty/resty/v2"
)

func (d *EdgeCOS) OfflineDownload(ctx context.Context, fileURL, targetPath string) (string, error) {
	fileURL = strings.TrimSpace(fileURL)
	if fileURL == "" {
		return "", errors.New("offline download URL is empty")
	}
	if parsed, err := url.Parse(fileURL); err != nil || parsed.Scheme == "" {
		return "", errors.New("offline download URL is invalid")
	}

	var resp OfflineSubmitResp
	_, err := d.requestContext(ctx, baseURL+"/offline/submit", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"url":        fileURL,
			"targetPath": normalizePath(targetPath),
		})
	}, &resp)
	if err != nil {
		return "", err
	}
	if taskID := resp.TaskIdentifier(); taskID != "" {
		return taskID, nil
	}
	if resp.Message != "" {
		return "", errors.New(resp.Message)
	}
	if resp.ErrorMsg != "" {
		return "", errors.New(resp.ErrorMsg)
	}
	return "", errors.New("offline download response did not include a task ID")
}

func (d *EdgeCOS) OfflineHistory(ctx context.Context) ([]OfflineTask, error) {
	var tasks []OfflineTask
	_, err := d.requestContext(ctx, baseURL+"/offline/history", http.MethodGet, func(req *resty.Request) {
		req.SetHeader("Cache-Control", "no-cache")
	}, &tasks)
	return tasks, err
}

func (d *EdgeCOS) DeleteOfflineTask(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("offline task ID is empty")
	}
	_, err := d.requestContext(ctx, baseURL+"/offline/tasks/"+url.PathEscape(taskID), http.MethodDelete, nil, nil)
	return err
}

func (d *EdgeCOS) OfflineTaskStatus(ctx context.Context, taskID string) (*OfflineTask, error) {
	tasks, err := d.OfflineHistory(ctx)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].ID == taskID {
			return &tasks[i], nil
		}
	}
	return nil, fmt.Errorf("offline task %s not found", taskID)
}

func (t OfflineTask) ProgressValue() float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(t.ProviderPercent, "%")), 64)
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func (t OfflineTask) TotalSize() int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(t.Size), 10, 64)
	return value
}

func (t OfflineTask) IsCompleted() bool {
	status := strings.ToLower(strings.TrimSpace(t.Status))
	state := strings.ToUpper(strings.TrimSpace(t.ProviderState))
	return status == "success" || status == "completed" || state == "TASK_SUCCESS"
}

func (t OfflineTask) Error() error {
	status := strings.ToLower(strings.TrimSpace(t.Status))
	state := strings.ToUpper(strings.TrimSpace(t.ProviderState))
	if t.ErrorMsg != "" {
		return errors.New(t.ErrorMsg)
	}
	if status == "failed" || status == "error" || status == "canceled" || status == "cancelled" || strings.Contains(state, "ERROR") || strings.Contains(state, "FAIL") || strings.Contains(state, "CANCEL") {
		if status == "" {
			status = strings.ToLower(state)
		}
		return errors.New(status)
	}
	return nil
}
