package edgecos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

type EdgeCOS struct {
	model.Storage
	Addition
	token string
}

func (d *EdgeCOS) Config() driver.Config {
	return config
}

func (d *EdgeCOS) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *EdgeCOS) Init(ctx context.Context) error {
	return d.login()
}

func (d *EdgeCOS) Drop(ctx context.Context) error {
	return nil
}

func (d *EdgeCOS) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	files, err := d.getFiles(dir.GetPath())
	if err != nil {
		return nil, err
	}
	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		return fileToObj(src), nil
	})
}

func (d *EdgeCOS) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return &model.Link{
		URL: d.downloadURL(file),
		Header: http.Header{
			"Cookie": []string{fmt.Sprintf("token=%s", d.token)},
		},
	}, nil
}

func (d *EdgeCOS) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	fullPath := d.getFullPath(parentDir, dirName)
	_, err := d.request(baseURL+"/mkdir", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{"path": fullPath})
	}, nil)
	if err != nil {
		return nil, err
	}
	return &model.Object{Name: dirName, Path: fullPath, IsFolder: true}, nil
}

func (d *EdgeCOS) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	_, err := d.request(baseURL+"/move", http.MethodPost, func(req *resty.Request) {
		req.SetBody(transferPayload(srcObj, dstDir))
	}, nil)
	return srcObj, err
}

func (d *EdgeCOS) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	payload := objectPayload(srcObj, "id", "oldPath")
	payload["newName"] = newName
	_, err := d.request(baseURL+"/rename", http.MethodPost, func(req *resty.Request) {
		req.SetBody(payload)
	}, nil)
	if err != nil {
		return nil, err
	}
	return &model.Object{
		ID:       srcObj.GetID(),
		Name:     newName,
		Size:     srcObj.GetSize(),
		Modified: srcObj.ModTime(),
		IsFolder: srcObj.IsDir(),
		Path:     normalizePath(path.Join(path.Dir(srcObj.GetPath()), newName)),
	}, nil
}

func (d *EdgeCOS) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	_, err := d.request(baseURL+"/copy", http.MethodPost, func(req *resty.Request) {
		req.SetBody(transferPayload(srcObj, dstDir))
	}, nil)
	return srcObj, err
}

func (d *EdgeCOS) Remove(ctx context.Context, obj model.Obj) error {
	_, err := d.request(baseURL+"/delete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(objectPayload(obj, "id", "path"))
	}, nil)
	return err
}

func (d *EdgeCOS) Put(ctx context.Context, dstDir model.Obj, streamer model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	fileSize := streamer.GetSize()
	if fileSize <= 0 || fileSize > maxUploadFileSize {
		return nil, fmt.Errorf("invalid file size %d: EdgeCOS accepts files between 1 byte and 100GB", fileSize)
	}

	dirPath := normalizePath(dstDir.GetPath())
	fileName := streamer.GetName()
	cachedFile, hashValue, err := cacheAndHash(streamer, fileSize)
	if err != nil {
		return nil, err
	}
	if closer, ok := cachedFile.(io.Closer); ok {
		defer closer.Close()
	}

	overwrite := streamer.GetExist() != nil
	if fileSize <= defaultMultipartPartSize {
		return d.putSmallFile(ctx, dirPath, fileName, fileSize, hashValue, overwrite, streamer.GetMimetype(), cachedFile, up)
	}
	return d.putLargeFile(ctx, dirPath, fileName, fileSize, hashValue, overwrite, cachedFile, up)
}

func cacheAndHash(streamer model.FileStreamer, fileSize int64) (model.File, string, error) {
	cachedFile, err := streamer.CacheFullAndWriter(nil, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to cache stream: %w", err)
	}
	if _, err := cachedFile.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("seek failed: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, cachedFile); err != nil {
		return nil, "", fmt.Errorf("hash calculation failed: %w", err)
	}
	if _, err := cachedFile.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("seek failed: %w", err)
	}
	return cachedFile, "sha256:" + hex.EncodeToString(hash.Sum(nil)) + ":" + strconv.FormatInt(fileSize, 10), nil
}

func (d *EdgeCOS) putSmallFile(ctx context.Context, dirPath, fileName string, fileSize int64, hashValue string, overwrite bool, mimeType string, cachedFile model.File, up driver.UpdateProgress) (model.Obj, error) {
	metadata := uploadMetadata(fileName, dirPath, fileSize, hashValue, overwrite)
	tokenResp, err := d.getUploadToken(fileName, dirPath, fileSize, hashValue, overwrite, false)
	if err != nil {
		return nil, err
	}
	if tokenResp.IsInstant() {
		completeResp, completeErr := d.completeInstant(tokenResp.Key, metadata)
		if completeErr == nil {
			up(100)
			return uploadedObject(fileName, dirPath, fileSize, completeResp), nil
		}
		if !isInstantMiss(completeErr) {
			return nil, completeErr
		}
		tokenResp, err = d.getUploadToken(fileName, dirPath, fileSize, hashValue, overwrite, true)
		if err != nil {
			return nil, err
		}
	}
	if tokenResp.URL == "" {
		return nil, errors.New("edgecos upload token did not include an upload URL")
	}
	if _, err := cachedFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek failed: %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(tokenResp.Method))
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(ctx, method, tokenResp.URL, cachedFile)
	if err != nil {
		return nil, err
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", mimeType)
	req.ContentLength = fileSize
	if err := executeUploadRequest(req); err != nil {
		return nil, err
	}
	up(90)

	metadata["key"] = tokenResp.Key
	var completeResp UploadCompleteResp
	_, err = d.request(baseURL+"/upload/complete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(metadata)
	}, &completeResp)
	if err != nil {
		return nil, err
	}
	up(100)
	return uploadedObject(fileName, dirPath, fileSize, completeResp), nil
}

func (d *EdgeCOS) getUploadToken(fileName, dirPath string, fileSize int64, hashValue string, overwrite, skipInstant bool) (UploadTokenResp, error) {
	params := map[string]string{
		"filename":  fileName,
		"filesize":  strconv.FormatInt(fileSize, 10),
		"path":      normalizePath(dirPath),
		"hash":      hashValue,
		"overwrite": strconv.FormatBool(overwrite),
	}
	if skipInstant {
		params["skipInstant"] = "true"
	}
	var tokenResp UploadTokenResp
	_, err := d.request(baseURL+"/upload/token", http.MethodGet, func(req *resty.Request) {
		req.SetQueryParams(params)
	}, &tokenResp)
	return tokenResp, err
}

func (d *EdgeCOS) completeInstant(key string, metadata base.Json) (UploadCompleteResp, error) {
	payload := cloneJSON(metadata)
	payload["key"] = key
	var completeResp UploadCompleteResp
	_, err := d.request(baseURL+"/upload/instant-complete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(payload)
	}, &completeResp)
	return completeResp, err
}

func (d *EdgeCOS) putLargeFile(ctx context.Context, dirPath, fileName string, fileSize int64, hashValue string, overwrite bool, cachedFile model.File, up driver.UpdateProgress) (model.Obj, error) {
	metadata := uploadMetadata(fileName, dirPath, fileSize, hashValue, overwrite)
	initResp, err := d.initMultipart(fileName, dirPath, fileSize, hashValue, overwrite, false)
	if err != nil {
		return nil, err
	}
	if initResp.IsInstant() {
		completeResp, completeErr := d.completeInstant(initResp.Key, metadata)
		if completeErr == nil {
			up(100)
			return uploadedObject(fileName, dirPath, fileSize, completeResp), nil
		}
		if !isInstantMiss(completeErr) {
			return nil, completeErr
		}
		initResp, err = d.initMultipart(fileName, dirPath, fileSize, hashValue, overwrite, true)
		if err != nil {
			return nil, err
		}
	}

	partSize := d.ChunkSize
	if partSize <= 0 {
		partSize = defaultMultipartPartSize
	}
	partCount := int((fileSize + partSize - 1) / partSize)
	uploadThread := d.UploadThread
	if uploadThread <= 0 {
		uploadThread = 3
	}
	if uploadThread > partCount {
		uploadThread = partCount
	}

	type partTask struct {
		number int
		offset int64
		size   int64
	}
	tasks := make(chan partTask, partCount)
	errCh := make(chan error, partCount)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	var uploadedBytes int64

	for worker := 0; worker < uploadThread; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				if utils.IsCanceled(ctx) {
					errCh <- ctx.Err()
					return
				}
				partURL, err := d.getMultipartURL(initResp.UploadID, initResp.Key, task.number)
				if err != nil {
					errCh <- err
					return
				}
				reader := io.NewSectionReader(cachedFile, task.offset, task.size)
				req, err := http.NewRequestWithContext(ctx, http.MethodPut, partURL, reader)
				if err != nil {
					errCh <- err
					return
				}
				req.Header.Set("Content-Type", "application/octet-stream")
				req.ContentLength = task.size
				if err := executeUploadRequest(req); err != nil {
					errCh <- fmt.Errorf("part %d upload failed: %w", task.number, err)
					return
				}
				progressMu.Lock()
				uploadedBytes += task.size
				up(float64(uploadedBytes) * 90 / float64(fileSize))
				progressMu.Unlock()
			}
		}()
	}

	for partNumber := 1; partNumber <= partCount; partNumber++ {
		offset := int64(partNumber-1) * partSize
		currentSize := partSize
		if remaining := fileSize - offset; remaining < currentSize {
			currentSize = remaining
		}
		tasks <- partTask{number: partNumber, offset: offset, size: currentSize}
	}
	close(tasks)
	wg.Wait()
	close(errCh)

	for uploadErr := range errCh {
		d.abortMultipart(initResp.UploadID, initResp.Key)
		return nil, uploadErr
	}

	payload := cloneJSON(metadata)
	payload["uploadId"] = initResp.UploadID
	payload["key"] = initResp.Key
	var completeResp UploadCompleteResp
	_, err = d.request(baseURL+"/upload/multipart/complete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(payload)
	}, &completeResp)
	if err != nil {
		d.abortMultipart(initResp.UploadID, initResp.Key)
		return nil, err
	}
	up(100)
	return uploadedObject(fileName, dirPath, fileSize, completeResp), nil
}

func (d *EdgeCOS) initMultipart(fileName, dirPath string, fileSize int64, hashValue string, overwrite, skipInstant bool) (MultipartInitResp, error) {
	payload := uploadInitPayload(fileName, dirPath, fileSize, hashValue, overwrite)
	if skipInstant {
		payload["skipInstant"] = true
	}
	var initResp MultipartInitResp
	_, err := d.request(baseURL+"/upload/multipart/init", http.MethodPost, func(req *resty.Request) {
		req.SetBody(payload)
	}, &initResp)
	return initResp, err
}

func (d *EdgeCOS) getMultipartURL(uploadID, key string, partNumber int) (string, error) {
	var urlsResp MultipartURLsResp
	_, err := d.request(baseURL+"/upload/multipart/urls", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"uploadId":    uploadID,
			"key":         key,
			"partNumbers": []int{partNumber},
		})
	}, &urlsResp)
	if err != nil {
		return "", err
	}
	partURL := urlsResp.URLs[strconv.Itoa(partNumber)]
	if partURL == "" {
		return "", fmt.Errorf("missing upload URL for part %d", partNumber)
	}
	return partURL, nil
}

func (d *EdgeCOS) abortMultipart(uploadID, key string) {
	_, _ = d.request(baseURL+"/upload/multipart/abort", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{"uploadId": uploadID, "key": key})
	}, nil)
}

func executeUploadRequest(req *http.Request) error {
	resp, err := base.HttpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func uploadedObject(fileName, dirPath string, fileSize int64, complete UploadCompleteResp) model.Obj {
	return &model.Object{
		ID:   complete.ID,
		Name: fileName,
		Size: fileSize,
		Path: normalizePath(path.Join(dirPath, fileName)),
	}
}

func cloneJSON(source base.Json) base.Json {
	cloned := make(base.Json, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
func (d *EdgeCOS) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	var quota QuotaResp
	_, err := d.request(baseURL+"/user/quota", http.MethodGet, nil, &quota)
	if err != nil {
		return nil, err
	}
	total, err := strconv.ParseInt(quota.Quota, 10, 64)
	if err != nil {
		return nil, err
	}
	used, err := strconv.ParseInt(quota.Used, 10, 64)
	if err != nil {
		return nil, err
	}
	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{TotalSpace: total, UsedSpace: used},
	}, nil
}

var _ driver.Driver = (*EdgeCOS)(nil)
