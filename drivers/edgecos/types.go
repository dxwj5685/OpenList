package edgecos

import (
	"encoding/json"
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
	FullPath     string      `json:"-"` // Constructed full path, not from API
}

func (f File) GetSize() int64 {
	n, _ := strconv.ParseInt(f.Size.String(), 10, 64)
	return n
}

type LoginResp struct {
	User struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	} `json:"user"`
}

type UserQuota struct {
	Quota string `json:"quota"`
	Used  string `json:"used"`
}

type UploadTokenResp struct {
	Method      string `json:"method"`
	URL         string `json:"url"`
	Key         string `json:"key"`
	QuickUpload bool   `json:"quickUpload"`
}

type MultipartInitResp struct {
	UploadID    string `json:"uploadId"`
	Key         string `json:"key"`
	QuickUpload bool   `json:"quickUpload"`
}

type MultipartURLsResp struct {
	URLs map[string]string `json:"urls"`
}

type ErrorResp struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
