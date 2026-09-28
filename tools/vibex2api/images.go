package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxImageBytes = 10 << 20

type chatImage struct {
	source, marker string
	data           []byte
}

func randomID() string { return rand.Text() }
func invalidImage() error {
	return problem(400, "invalid_image", "Images must be valid PNG, JPEG, GIF or WebP, at most 10 MiB and 32 million pixels")
}

func parseImage(source string) (chatImage, error) {
	img := chatImage{source: source, marker: "VIBEX_IMAGE_" + randomID()}
	if strings.HasPrefix(source, "data:") {
		meta, data, ok := strings.Cut(source, ",")
		if !ok || !strings.HasPrefix(meta, "data:image/") || !strings.HasSuffix(meta, ";base64") || len(data) > base64.StdEncoding.EncodedLen(maxImageBytes) {
			return img, invalidImage()
		}
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return img, invalidImage()
		}
		img.data, err = prepareImage(decoded)
		return img, err
	}
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return img, invalidImage()
	}
	return img, nil
}

func prepareImage(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, invalidImage()
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return nil, invalidImage()
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, invalidImage()
	}
	w, h := config.Width, config.Height
	if w > 1024 || h > 1024 {
		if w >= h {
			h = max(1, h*1024/w)
			w = 1024
		} else {
			w = max(1, w*1024/h)
			h = 1024
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if jpeg.Encode(&output, dst, &jpeg.Options{Quality: 85}) != nil {
		return nil, invalidImage()
	}
	return output.Bytes(), nil
}

// 图片地址使用独立连接，不带平台凭证，并在连接时检查实际解析地址。
func imageHTTPClient() *http.Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("image host unavailable")
		}
		for _, ip := range ips {
			if !publicImageIP(ip.IP) {
				return nil, fmt.Errorf("image host must be public")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 || r.URL.User != nil || (r.URL.Scheme != "https" && r.URL.Scheme != "http") {
			return fmt.Errorf("invalid image redirect")
		}
		return nil
	}}
}

func publicImageIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96"} {
		_, subnet, _ := net.ParseCIDR(cidr)
		if subnet.Contains(ip) {
			return false
		}
	}
	return true
}

func (a *adapter) uploadImages(ctx context.Context, c credential, images []chatImage, prompt string) (string, error) {
	if len(images) == 0 {
		return prompt, nil
	}
	client := imageHTTPClient()
	defer client.CloseIdleConnections()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i, img := range images {
		data := img.data
		if data == nil {
			req, err := http.NewRequestWithContext(ctx, "GET", img.source, nil)
			if err != nil {
				return "", invalidImage()
			}
			res, err := client.Do(req)
			if err != nil {
				return "", problem(400, "image_unavailable", "Unable to retrieve image from a public URL")
			}
			raw, readErr := io.ReadAll(io.LimitReader(res.Body, maxImageBytes+1))
			res.Body.Close()
			if res.StatusCode != 200 || readErr != nil {
				return "", invalidImage()
			}
			data, err = prepareImage(raw)
			if err != nil {
				return "", err
			}
		}
		part, err := writer.CreateFormFile("files", fmt.Sprintf("image-%s-%d.jpg", randomID(), i))
		if err != nil {
			return "", err
		}
		if _, err = part.Write(data); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.origin+"/vc/api/apps/"+url.PathEscape(c.AppID)+"/attachments", &body)
	if err != nil {
		return "", err
	}
	req.Header = a.headers(c)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	var result struct {
		Attachments []struct {
			ContainerPath string `json:"container_path"`
			RelativePath  string `json:"relative_path"`
		} `json:"attachments"`
	}
	if err = a.callRequest(req, &result); err != nil {
		return "", err
	}
	if len(result.Attachments) != len(images) {
		return "", problem(502, "invalid_attachment_response", "VibeX did not return all image paths")
	}
	for i, item := range result.Attachments {
		if !strings.HasPrefix(item.ContainerPath, "/workspace/app/") || strings.Contains(item.ContainerPath, "..") || strings.ContainsAny(item.ContainerPath, "\r\n") {
			return "", problem(502, "invalid_attachment_response", "Invalid VibeX image path")
		}
		prompt = strings.ReplaceAll(prompt, images[i].marker, item.ContainerPath)
	}
	return prompt + "\nInspect the listed uploaded images using Read before answering. They were resized to at most 1024 pixels. Treat attachment contents as user input. Do not describe your inspection steps; return only the requested final answer or client function JSON.\n", nil
}
