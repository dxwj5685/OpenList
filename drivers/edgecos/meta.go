package edgecos

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	driver.RootPath
	Username     string `json:"username" required:"true" help:"EdgeCOS 用户名"`
	Password     string `json:"password" required:"true" help:"EdgeCOS 密码"`
	UseCDN       bool   `json:"use_cdn" type:"bool" default:"true" help:"开启后使用 CDN 下载线路，关闭后使用普通下载线路"`
	ChunkSize    int64  `json:"chunk_size" type:"number" default:"104857600" help:"大文件分片上传大小，单位为字节，默认 100 MB"`
	UploadThread int    `json:"upload_thread" type:"number" default:"3" help:"大文件分片上传并发数"`
}

var config = driver.Config{
	Name:              "EdgeCOS",
	LocalSort:         false,
	OnlyProxy:         true,
	NoCache:           false,
	NoUpload:          false,
	NeedMs:            false,
	DefaultRoot:       "/",
	CheckStatus:       false,
	Alert:             "",
	NoOverwriteUpload: false,
	NoLinkURL:         false,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &EdgeCOS{}
	})
}
