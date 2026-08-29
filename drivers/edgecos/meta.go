package edgecos

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	driver.RootPath
	Username     string `json:"username" required:"true"`
	Password     string `json:"password" required:"true"`
	DownloadLine string `json:"download_line" type:"select" options:"cdn,free" default:"cdn" help:"Download line used by the EdgeCOS file API"`
	ChunkSize    int64  `json:"chunk_size" type:"number" default:"104857600" help:"Chunk size for multipart upload (in bytes, default 100MB)"`
	UploadThread int    `json:"upload_thread" type:"number" default:"3" help:"Concurrent upload threads for large files"`
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
