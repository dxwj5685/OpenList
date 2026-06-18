package edgecos

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
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
	dirPath := dir.GetPath()
	if dirPath == "" || dirPath == "root" {
		dirPath = "/"
	}

	files, err := d.getFiles(dirPath)
	if err != nil {
		return nil, err
	}

	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		return fileToObj(src), nil
	})
}

func (d *EdgeCOS) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	// EdgeCOS download uses file path, not fileId
	url := fmt.Sprintf("%s/download?path=%s", baseURL, file.GetPath())
	return &model.Link{
		URL: url,
		Header: http.Header{
			"Cookie": []string{fmt.Sprintf("token=%s", d.token)},
		},
	}, nil
}

func (d *EdgeCOS) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	fullPath := d.getFullPath(parentDir, dirName)

	_, err := d.request(baseURL+"/mkdir", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"path": fullPath,
		})
	}, nil)

	if err != nil {
		return nil, err
	}

	return &model.Object{
		Name:     dirName,
		Path:     fullPath,
		IsFolder: true,
	}, nil
}

func (d *EdgeCOS) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	// move uses source path and target directory (not IDs)
	sourcePath := srcObj.GetPath()
	targetPath := dstDir.GetPath()
	if targetPath == "" {
		targetPath = "/"
	}
	// targetPath should be a directory (ensure trailing slash)
	if !strings.HasSuffix(targetPath, "/") {
		targetPath += "/"
	}

	_, err := d.request(baseURL+"/move", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"sourcePath": sourcePath,
			"targetPath": targetPath,
		})
	}, nil)

	return srcObj, err
}

func (d *EdgeCOS) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	_, err := d.request(baseURL+"/rename", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"id":      srcObj.GetID(),
			"newName": newName,
		})
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
	}, nil
}

func (d *EdgeCOS) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	// copy uses source path and target directory (not IDs)
	sourcePath := srcObj.GetPath()
	targetPath := dstDir.GetPath()
	if targetPath == "" {
		targetPath = "/"
	}
	// targetPath should be a directory (ensure trailing slash)
	if !strings.HasSuffix(targetPath, "/") {
		targetPath += "/"
	}

	_, err := d.request(baseURL+"/copy", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"sourcePath": sourcePath,
			"targetPath": targetPath,
		})
	}, nil)

	return srcObj, err
}

func (d *EdgeCOS) Remove(ctx context.Context, obj model.Obj) error {
	// delete uses a single id (not an array)
	_, err := d.request(baseURL+"/delete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"id": obj.GetID(),
		})
	}, nil)

	return err
}

func (d *EdgeCOS) Put(ctx context.Context, dstDir model.Obj, streamer model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	dirPath := dstDir.GetPath()
	if dirPath == "" || dirPath == "root" {
		dirPath = "/"
	}

	fileSize := streamer.GetSize()
	fileName := streamer.GetName()

	// Small file upload (< 100MB)
	const maxSmallFileSize = 100 * 1024 * 1024
	if fileSize < maxSmallFileSize {
		return d.putSmallFile(ctx, dirPath, fileName, fileSize, streamer, up)
	}

	// Large file multipart upload (>= 100MB)
	return d.putLargeFile(ctx, dirPath, fileName, fileSize, streamer, up)
}

func (d *EdgeCOS) putSmallFile(ctx context.Context, dirPath, fileName string, fileSize int64, streamer model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	// Get upload token
	var tokenResp UploadTokenResp
	_, err := d.request(baseURL+"/upload/token", http.MethodGet, func(req *resty.Request) {
		req.SetQueryParams(map[string]string{
			"filename": fileName,
			"filesize": strconv.FormatInt(fileSize, 10),
			"path":     dirPath,
			"hash":     "",
		})
	}, &tokenResp)

	if err != nil {
		return nil, err
	}

	// Check for quick upload (file already exists)
	if tokenResp.QuickUpload {
		up(100)
		return &model.Object{
			Name: fileName,
			Size: fileSize,
			Path: dirPath + "/" + fileName,
		}, nil
	}

	// Upload file to presigned URL
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, tokenResp.URL, streamer)
	if err != nil {
		return nil, err
	}

	// EdgeCOS presigned URLs expect application/octet-stream
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fileSize

	resp, err := base.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed: %s", string(body))
	}

	up(90)

	// Complete upload
	_, err = d.request(baseURL+"/upload/complete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"key":      tokenResp.Key,
			"filename": fileName,
			"path":     dirPath,
			"size":     fileSize,
			"hash":     "",
		})
	}, nil)

	if err != nil {
		return nil, err
	}

	up(100)

	return &model.Object{
		Name: fileName,
		Size: fileSize,
		Path: dirPath + "/" + fileName,
	}, nil
}

func (d *EdgeCOS) putLargeFile(ctx context.Context, dirPath, fileName string, fileSize int64, streamer model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	// Initialize multipart upload
	var initResp MultipartInitResp
	_, err := d.request(baseURL+"/upload/multipart/init", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"filename": fileName,
			"filesize": fileSize,
			"path":     dirPath,
			"hash":     "",
		})
	}, &initResp)

	if err != nil {
		return nil, err
	}

	// Check for quick upload
	if initResp.QuickUpload {
		up(100)
		return &model.Object{
			Name: fileName,
			Size: fileSize,
			Path: dirPath + "/" + fileName,
		}, nil
	}

	// Use configured chunk size or default to 10MB
	partSize := d.ChunkSize
	if partSize <= 0 {
		partSize = 10 * 1024 * 1024
	}
	partCount := int(math.Ceil(float64(fileSize) / float64(partSize)))

	// Get upload URLs for all parts
	partNumbers := make([]int, partCount)
	for i := 0; i < partCount; i++ {
		partNumbers[i] = i + 1
	}

	var urlsResp MultipartURLsResp
	_, err = d.request(baseURL+"/upload/multipart/urls", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"uploadId":    initResp.UploadID,
			"key":         initResp.Key,
			"partNumbers": partNumbers,
		})
	}, &urlsResp)

	if err != nil {
		return nil, err
	}

	// Cache the full stream so parts can be read concurrently via ReadAt.
	// A stream is sequential and cannot be read concurrently, so we must
	// cache it to a seekable File first.
	cacheFile, err := streamer.CacheFullAndWriter(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to cache stream: %w", err)
	}

	// Upload parts with concurrency
	uploadThread := d.UploadThread
	if uploadThread <= 0 {
		uploadThread = 3
	}
	if uploadThread > partCount {
		uploadThread = partCount
	}

	// Channel for part upload tasks
	type partTask struct {
		partNum int
		offset  int64
		size    int64
	}

	tasks := make(chan partTask, partCount)
	errCh := make(chan error, partCount)
	var wg sync.WaitGroup

	// Progress tracking
	uploadedParts := make([]bool, partCount)
	var progressMu sync.Mutex

	// Worker goroutines
	for w := 0; w < uploadThread; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				if utils.IsCanceled(ctx) {
					errCh <- ctx.Err()
					return
				}

				partURL := urlsResp.URLs[strconv.Itoa(task.partNum)]
				if partURL == "" {
					errCh <- fmt.Errorf("missing upload URL for part %d", task.partNum)
					return
				}

				// Read this part's data from the cached file at the given offset.
				// io.SectionReader is safe for concurrent use since each worker
				// gets its own SectionReader over the shared ReaderAt.
				sectionReader := io.NewSectionReader(cacheFile, task.offset, task.size)

				req, err := http.NewRequestWithContext(ctx, http.MethodPut, partURL, sectionReader)
				if err != nil {
					errCh <- err
					return
				}

				req.Header.Set("Content-Type", "application/octet-stream")
				req.ContentLength = task.size

				resp, err := base.HttpClient.Do(req)
				if err != nil {
					errCh <- err
					return
				}
				resp.Body.Close()

				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
					errCh <- fmt.Errorf("part %d upload failed with status %d", task.partNum, resp.StatusCode)
					return
				}

				// Update progress
				progressMu.Lock()
				uploadedParts[task.partNum-1] = true
				uploaded := 0
				for _, done := range uploadedParts {
					if done {
						uploaded++
					}
				}
				progress := float64(uploaded) * 90 / float64(partCount)
				up(progress)
				progressMu.Unlock()
			}
		}()
	}

	// Send tasks to workers
	go func() {
		for i := 1; i <= partCount; i++ {
			offset := int64(i-1) * partSize
			currentPartSize := partSize
			if i == partCount {
				currentPartSize = fileSize - offset
			}

			tasks <- partTask{
				partNum: i,
				offset:  offset,
				size:    currentPartSize,
			}
		}
		close(tasks)
	}()

	// Wait for all uploads to complete
	wg.Wait()
	close(errCh)

	// Collect all errors from the channel
	var uploadErrors []error
	for err := range errCh {
		uploadErrors = append(uploadErrors, err)
	}

	// Return the first error if any occurred
	if len(uploadErrors) > 0 {
		return nil, uploadErrors[0]
	}

	// Complete multipart upload
	_, err = d.request(baseURL+"/upload/multipart/complete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"uploadId": initResp.UploadID,
			"key":      initResp.Key,
		})
	}, nil)

	if err != nil {
		return nil, err
	}

	// Final complete
	_, err = d.request(baseURL+"/upload/complete", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"key":      initResp.Key,
			"filename": fileName,
			"path":     dirPath,
			"size":     fileSize,
			"hash":     "",
		})
	}, nil)

	if err != nil {
		return nil, err
	}

	up(100)

	return &model.Object{
		Name: fileName,
		Size: fileSize,
		Path: dirPath + "/" + fileName,
	}, nil
}

func (d *EdgeCOS) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	var quota UserQuota
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
		DiskUsage: model.DiskUsage{
			TotalSpace: total,
			UsedSpace:  used,
		},
	}, nil
}

var _ driver.Driver = (*EdgeCOS)(nil)
