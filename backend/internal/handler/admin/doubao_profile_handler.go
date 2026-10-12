package admin

import (
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const maxDoubaoSessionSnapshotBytes = 64 << 10

var doubaoSessionProfileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ListDoubaoSessionProfiles 返回会话别名，不返回任何 Cookie 或快照字段。
func (h *AccountHandler) ListDoubaoSessionProfiles(c *gin.Context) {
	profiles, err := service.ListDoubaoSessionProfiles(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"profiles": profiles})
}

// UploadDoubaoSessionProfile 接收 Mac 端导出的 session.json 快照。
func (h *AccountHandler) UploadDoubaoSessionProfile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDoubaoSessionSnapshotBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		response.BadRequest(c, "上传请求无效或超过大小限制")
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	profile := strings.TrimSpace(c.PostForm("profile"))
	if !doubaoSessionProfileName.MatchString(profile) || profile == "default" {
		response.BadRequest(c, "会话名称仅支持字母、数字、短横线和下划线，且不能使用 default")
		return
	}
	file, _, err := c.Request.FormFile("session")
	if err != nil {
		response.BadRequest(c, "请选择有效的豆包 session.json 文件")
		return
	}
	defer file.Close()
	snapshot, err := io.ReadAll(io.LimitReader(file, maxDoubaoSessionSnapshotBytes+1))
	if err != nil {
		response.BadRequest(c, "无法读取豆包会话文件")
		return
	}
	if len(snapshot) > maxDoubaoSessionSnapshotBytes {
		response.BadRequest(c, "豆包会话文件不能超过 64 KiB")
		return
	}
	if err := service.UploadDoubaoSessionProfile(c.Request.Context(), profile, snapshot); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"name": profile})
}

// DeleteDoubaoSessionProfile 删除指定的会话快照。
func (h *AccountHandler) DeleteDoubaoSessionProfile(c *gin.Context) {
	profile := strings.TrimSpace(c.Param("profile"))
	if err := service.DeleteDoubaoSessionProfile(c.Request.Context(), profile); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"name": profile, "deleted": true})
}
