package edgecos

import (
	"errors"
	"fmt"
	"net/http"
	"path"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

const (
	baseURL = "https://user.edgecos.com/api"
)

func (d *EdgeCOS) request(url string, method string, callback base.ReqCallback, resp interface{}) ([]byte, error) {
	req := base.RestyClient.R()
	req.SetHeader("Cookie", fmt.Sprintf("token=%s", d.token))

	if callback != nil {
		callback(req)
	}

	if resp != nil {
		req.SetResult(resp)
	}

	var e ErrorResp
	req.SetError(&e)

	res, err := req.Execute(method, url)
	if err != nil {
		return nil, err
	}

	if res.IsError() {
		return nil, errors.New(e.Message)
	}

	return res.Body(), nil
}

func (d *EdgeCOS) getFiles(dirPath string) ([]File, error) {
	var files []File
	_, err := d.request(baseURL+"/files", http.MethodGet, func(req *resty.Request) {
		req.SetQueryParam("path", dirPath)
	}, &files)

	// Construct full path for each file
	for i := range files {
		files[i].FullPath = path.Join(dirPath, files[i].Basename)
	}

	return files, err
}

func fileToObj(f File) model.Obj {
	return &model.Object{
		ID:       f.ID,
		Name:     f.Basename,
		Size:     f.GetSize(),
		Modified: f.LastMod,
		IsFolder: f.Type == "directory",
		Path:     f.FullPath, // Use constructed full path
	}
}

func (d *EdgeCOS) login() error {
	req := base.RestyClient.R()
	req.SetBody(base.Json{
		"username": d.Username,
		"password": d.Password,
	})

	var resp LoginResp
	req.SetResult(&resp)

	var e ErrorResp
	req.SetError(&e)

	res, err := req.Post(baseURL + "/login")
	if err != nil {
		return err
	}

	if res.IsError() {
		return errors.New(e.Message)
	}

	// Extract token from Set-Cookie header
	cookies := res.Cookies()
	for _, cookie := range cookies {
		if cookie.Name == "token" {
			d.token = cookie.Value
			break
		}
	}

	if d.token == "" {
		return errors.New("failed to get token from login response")
	}

	return nil
}

func (d *EdgeCOS) getFullPath(dir model.Obj, name string) string {
	if dir.GetID() == "root" || dir.GetPath() == "/" {
		return "/" + name
	}
	return path.Join(dir.GetPath(), name)
}
