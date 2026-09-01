package edgecos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

type File struct {
	ID           string      `json:"id"`
	Filename     string      `json:"filename"`
	Basename     string      `json:"basename"`
	LastMod      time.Time   `json:"lastmod"`
	Size         json.Number `json:"size"`
	Type         string      `json:"type"`
	Thumbnail    *string     `json:"thumbnail"`
	HasThumbnail bool        `json:"hasThumbnail"`
	FullPath     string      `json:"-"`
}

func (f File) GetSize() int64 {
	n, _ := strconv.ParseInt(f.Size.String(), 10, 64)
	return n
}

type FileListResp struct {
	Items    []File `json:"items"`
	Total    int    `json:"total"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	HasMore  bool   `json:"hasMore"`
	isLegacy bool
}

func (r *FileListResp) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if data[0] == '[' {
		if err := json.Unmarshal(data, &r.Items); err != nil {
			return err
		}
		r.Total = len(r.Items)
		r.HasMore = false
		r.isLegacy = true
		return nil
	}
	type alias FileListResp
	return json.Unmarshal(data, (*alias)(r))
}

type LoginResp struct {
	User struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	} `json:"user"`
}

type QuotaResp struct {
	Quota string `json:"quota"`
	Used  string `json:"used"`
}

type UploadTokenResp struct {
	Method      string `json:"method"`
	URL         string `json:"url"`
	Key         string `json:"key"`
	Instant     bool   `json:"instant"`
	QuickUpload bool   `json:"quickUpload"`
}

func (r UploadTokenResp) IsInstant() bool {
	return r.Instant || r.QuickUpload
}

type MultipartInitResp struct {
	UploadID    string `json:"uploadId"`
	Key         string `json:"key"`
	Instant     bool   `json:"instant"`
	QuickUpload bool   `json:"quickUpload"`
}

func (r MultipartInitResp) IsInstant() bool {
	return r.Instant || r.QuickUpload
}

type MultipartURLsResp struct {
	URLs map[string]string `json:"urls"`
}

type UploadCompleteResp struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type ErrorResp struct {
	Code    string `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type OfflineTask struct {
	ID              string    `json:"id"`
	URL             string    `json:"url"`
	TargetPath      string    `json:"targetPath"`
	ProviderTaskID  string    `json:"providerTaskId"`
	ProviderState   string    `json:"providerState"`
	ProviderPercent string    `json:"providerPercent"`
	Status          string    `json:"status"`
	Size            string    `json:"size"`
	ErrorMsg        string    `json:"errorMsg"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type OfflineSubmitResp struct {
	ID       string      `json:"id"`
	TaskID   string      `json:"taskId"`
	Task     OfflineTask `json:"task"`
	Data     OfflineTask `json:"data"`
	Message  string      `json:"message"`
	ErrorMsg string      `json:"errorMsg"`
}

func (r OfflineSubmitResp) TaskIdentifier() string {
	for _, id := range []string{r.ID, r.TaskID, r.Task.ID, r.Data.ID} {
		if id != "" {
			return id
		}
	}
	return ""
}
