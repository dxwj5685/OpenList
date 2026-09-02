package edgecos

import (
	"context"
	"errors"
	"fmt"

	edgecosdriver "github.com/OpenListTeam/OpenList/v4/drivers/edgecos"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type EdgeCOS struct{}

func (e *EdgeCOS) Name() string { return "EdgeCOS" }

func (e *EdgeCOS) Items() []model.SettingItem { return nil }

func (e *EdgeCOS) Init() (string, error) { return "ok", nil }

func (e *EdgeCOS) IsReady() bool { return true }

func (e *EdgeCOS) Run(task *tool.DownloadTask) error { return errs.NotSupport }

func (e *EdgeCOS) AddURL(args *tool.AddUrlArgs) (string, error) {
	storage, actualPath, err := op.GetStorageAndActualPath(args.TempDir)
	if err != nil {
		return "", err
	}
	driver, ok := storage.(*edgecosdriver.EdgeCOS)
	if !ok {
		return "", errors.New("EdgeCOS 离线下载只支持将目标设置为 EdgeCOS 存储")
	}
	ctx := args.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := op.MakeDir(ctx, storage, actualPath); err != nil {
		return "", err
	}
	taskID, err := driver.OfflineDownload(ctx, args.Url, actualPath)
	if err != nil {
		return "", fmt.Errorf("提交 EdgeCOS 离线下载任务失败: %w", err)
	}
	return taskID, nil
}

func (e *EdgeCOS) Remove(task *tool.DownloadTask) error {
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return err
	}
	driver, ok := storage.(*edgecosdriver.EdgeCOS)
	if !ok {
		return errors.New("EdgeCOS 离线下载只支持将目标设置为 EdgeCOS 存储")
	}
	return driver.DeleteOfflineTask(task.Ctx(), task.GID)
}

func (e *EdgeCOS) Status(task *tool.DownloadTask) (*tool.Status, error) {
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return nil, err
	}
	driver, ok := storage.(*edgecosdriver.EdgeCOS)
	if !ok {
		return nil, errors.New("EdgeCOS 离线下载只支持将目标设置为 EdgeCOS 存储")
	}
	offlineTask, err := driver.OfflineTaskStatus(task.Ctx(), task.GID)
	if err != nil {
		return nil, err
	}
	status := &tool.Status{
		TotalBytes: offlineTask.TotalSize(),
		Progress:   offlineTask.ProgressValue(),
		Completed:  offlineTask.IsCompleted(),
		Status:     offlineTask.Status,
		Err:        offlineTask.Error(),
	}
	if status.Status == "" {
		status.Status = offlineTask.ProviderState
	}
	if status.Status == "" {
		status.Status = "EdgeCOS 离线下载处理中"
	}
	return status, nil
}

func init() { tool.Tools.Add(&EdgeCOS{}) }
