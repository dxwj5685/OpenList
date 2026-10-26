package edgecos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/go-resty/resty/v2"
)

const (
	baseURL                  = "https://user.edgecos.com/api"
	fileListPageSize         = 40
	defaultMultipartPartSize = 100 * 1024 * 1024
	maxUploadFileSize        = 100 * 1024 * 1024 * 1024
)

func (d *EdgeCOS) request(ctx context.Context, endpoint string, method string, callback base.ReqCallback, resp interface{}) ([]byte, error) {
	var body []byte
	err := d.withAuthentication(ctx, func(token string) error {
		req := base.RestyClient.R().SetContext(ctx)
		req.SetHeader("Cookie", fmt.Sprintf("token=%s", token))
		if callback != nil {
			callback(req)
		}
		if resp != nil {
			req.SetResult(resp)
		}
		var apiErr ErrorResp
		req.SetError(&apiErr)
		res, err := req.Execute(method, endpoint)
		if err != nil {
			return err
		}
		if res.IsError() {
			return responseError(res, apiErr)
		}
		body = res.Body()
		return nil
	})
	return body, err
}

func responseError(res *resty.Response, apiErr ErrorResp) *APIError {
	message := apiErr.Message
	if message == "" {
		message = apiErr.Error
	}
	if message == "" {
		message = strings.TrimSpace(string(res.Body()))
	}
	if message == "" {
		message = res.Status()
	}
	return &APIError{StatusCode: res.StatusCode(), Code: apiErr.Code, Message: message}
}

func (d *EdgeCOS) withAuthentication(ctx context.Context, request func(string) error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		token := d.sessionToken()
		err := request(token)
		if attempt != 0 || !isAuthenticationError(err) {
			return err
		}
		if err := d.refreshSession(ctx, token); err != nil {
			return fmt.Errorf("edgecos reauthentication failed: %w", err)
		}
	}
}

func isAuthenticationError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode != http.StatusUnauthorized {
			return false
		}
		switch apiErr.Code {
		case "", "AUTH_REQUIRED", "AUTH_EXPIRED", "AUTH_INVALID":
			return true
		default:
			return false
		}
	}
	var status net.HttpStatusCodeError
	return errors.As(err, &status) && status == http.StatusUnauthorized
}

func (d *EdgeCOS) sessionToken() string {
	d.tokenMu.RLock()
	defer d.tokenMu.RUnlock()
	return d.token
}

func (d *EdgeCOS) refreshSession(ctx context.Context, rejectedToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := d.loginGroup.DoChan("login", func() (struct{}, error) {
		// Another failed request may already have replaced the rejected token.
		if token := d.sessionToken(); token != "" && token != rejectedToken {
			return struct{}{}, nil
		}
		// A canceled waiter must not cancel authentication for other requests.
		loginCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), base.DefaultTimeout)
		defer cancel()
		return struct{}{}, d.authenticate(loginCtx)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-result:
		return result.Err
	}
}

func (d *EdgeCOS) getFiles(ctx context.Context, dirPath string) ([]File, error) {
	dirPath = normalizePath(dirPath)
	files := make([]File, 0)
	offset := 0

	for {
		var page FileListResp
		_, err := d.request(ctx, baseURL+"/files", http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(map[string]string{
				"path":   dirPath,
				"limit":  strconv.Itoa(fileListPageSize),
				"offset": strconv.Itoa(offset),
				"sort":   "name-asc",
			})
		}, &page)
		if err != nil {
			return nil, err
		}

		for i := range page.Items {
			page.Items[i].FullPath = normalizeFilePath(dirPath, page.Items[i])
		}
		files = append(files, page.Items...)

		if page.isLegacy || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return nil, errors.New("edgecos file list returned hasMore without items")
		}
		offset += len(page.Items)
	}

	return files, nil
}

func normalizePath(remotePath string) string {
	if remotePath == "" || remotePath == "root" {
		return "/"
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(remotePath, "/"))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func normalizeFilePath(dirPath string, file File) string {
	if file.Filename != "" {
		return normalizePath(file.Filename)
	}
	return normalizePath(path.Join(dirPath, file.Basename))
}

func fileToObj(f File) model.Obj {
	return &model.Object{
		ID:       f.ID,
		Name:     f.Basename,
		Size:     f.GetSize(),
		Modified: f.LastMod,
		IsFolder: f.Type == "directory",
		Path:     f.FullPath,
	}
}

func (d *EdgeCOS) login(ctx context.Context) error {
	return d.refreshSession(ctx, d.sessionToken())
}

func (d *EdgeCOS) authenticate(ctx context.Context) error {
	req := base.RestyClient.R().SetContext(ctx)
	req.SetBody(base.Json{
		"username": d.Username,
		"password": d.Password,
	})

	var resp LoginResp
	req.SetResult(&resp)

	var apiErr ErrorResp
	req.SetError(&apiErr)

	res, err := req.Post(baseURL + "/login")
	if err != nil {
		return err
	}
	if res.IsError() {
		return responseError(res, apiErr)
	}

	var token string
	for _, cookie := range res.Cookies() {
		if cookie.Name == "token" {
			token = cookie.Value
			break
		}
	}
	if token == "" {
		return errors.New("failed to get token from login response")
	}
	d.tokenMu.Lock()
	d.token = token
	d.tokenMu.Unlock()

	return nil
}

func (d *EdgeCOS) getFullPath(dir model.Obj, name string) string {
	return normalizePath(path.Join(normalizePath(dir.GetPath()), name))
}

func objectPayload(obj model.Obj, idKey, pathKey string) base.Json {
	if obj.GetID() != "" {
		return base.Json{idKey: obj.GetID()}
	}
	return base.Json{pathKey: normalizePath(obj.GetPath())}
}

func transferPayload(srcObj, dstDir model.Obj) base.Json {
	payload := objectPayload(srcObj, "sourceId", "sourcePath")
	payload["targetDir"] = normalizePath(dstDir.GetPath())
	return payload
}

func (d *EdgeCOS) downloadURL(file model.Obj) string {
	query := url.Values{}
	if file.GetID() != "" {
		query.Set("id", file.GetID())
	} else {
		query.Set("path", normalizePath(file.GetPath()))
	}
	line := "free"
	if d.UseCDN {
		line = "cdn"
	}
	query.Set("line", line)
	return baseURL + "/download?" + query.Encode()
}

func uploadMetadata(fileName, dirPath string, fileSize int64, hash string, overwrite bool) base.Json {
	return base.Json{
		"filename":  fileName,
		"path":      normalizePath(dirPath),
		"size":      fileSize,
		"hash":      hash,
		"overwrite": overwrite,
	}
}

func uploadInitPayload(fileName, dirPath string, fileSize int64, hash string, overwrite bool) base.Json {
	payload := uploadMetadata(fileName, dirPath, fileSize, hash, overwrite)
	payload["filesize"] = fileSize
	delete(payload, "size")
	return payload
}

func isInstantMiss(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && strings.EqualFold(apiErr.Code, "INSTANT_MISS")
}
