// internal/weixin/api.go — 微信 ilink bot HTTP API 客户端
package weixin

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crypto/md5"
	mrand "math/rand"

	"github.com/google/uuid"

	"OTTClaw/internal/logger"
)

const (
	DefaultBaseURL    = "https://ilinkai.weixin.qq.com"
	DefaultCDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"
	LongPollTimeout   = 35
	APITimeout        = 15
	QRPollTimeout     = 35
	BotType           = "3"
	UploadMaxRetries  = 3
)

// API is a stateless HTTP client for the Weixin ilink bot API.
type API struct {
	BaseURL    string
	Token      string
	CDNBaseURL string
}

func NewAPI(baseURL, token, cdnBaseURL string) *API {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if cdnBaseURL == "" {
		cdnBaseURL = DefaultCDNBaseURL
	}
	return &API{BaseURL: baseURL, Token: token, CDNBaseURL: cdnBaseURL}
}

func (a *API) post(endpoint string, body any, timeout time.Duration) (map[string]any, error) {
	data, _ := json.Marshal(body)
	u := ensureTrailingSlash(a.BaseURL) + endpoint
	req, err := http.NewRequest("POST", u, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", randomWechatUIN())
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	c := &http.Client{Timeout: timeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *API) GetUpdates(getUpdatesBuf string) (map[string]any, error) {
	return a.post("ilink/bot/getupdates", map[string]any{
		"get_updates_buf": getUpdatesBuf,
	}, time.Duration(LongPollTimeout+5)*time.Second)
}

func (a *API) SendText(to, text, contextToken string) error {
	_, err := a.post("ilink/bot/sendmessage", map[string]any{
		"msg": map[string]any{
			"from_user_id": "", "to_user_id": to,
			"client_id": uuid.New().String()[:16], "message_type": 2, "message_state": 2,
			"item_list":     []any{map[string]any{"type": 1, "text_item": map[string]any{"text": text}}},
			"context_token": contextToken,
		},
	}, time.Duration(APITimeout)*time.Second)
	return err
}

func (a *API) SendImageItem(to, contextToken, encryptParam, aesKeyB64 string, cipherSize int) error {
	_, err := a.post("ilink/bot/sendmessage", map[string]any{
		"msg": map[string]any{
			"from_user_id": "", "to_user_id": to,
			"client_id": uuid.New().String()[:16], "message_type": 2, "message_state": 2,
			"item_list": []any{map[string]any{"type": 2, "image_item": map[string]any{
				"media": map[string]any{"encrypt_query_param": encryptParam, "aes_key": aesKeyB64, "encrypt_type": 1}, "mid_size": cipherSize,
			}}},
			"context_token": contextToken,
		},
	}, time.Duration(APITimeout)*time.Second)
	return err
}

func (a *API) SendFileItem(to, contextToken, encryptParam, aesKeyB64, fileName string, fileSize int) error {
	_, err := a.post("ilink/bot/sendmessage", map[string]any{
		"msg": map[string]any{
			"from_user_id": "", "to_user_id": to,
			"client_id": uuid.New().String()[:16], "message_type": 2, "message_state": 2,
			"item_list": []any{map[string]any{"type": 4, "file_item": map[string]any{
				"media": map[string]any{"encrypt_query_param": encryptParam, "aes_key": aesKeyB64, "encrypt_type": 1},
				"file_name": fileName, "len": fmt.Sprintf("%d", fileSize),
			}}},
			"context_token": contextToken,
		},
	}, time.Duration(APITimeout)*time.Second)
	return err
}

// GetConfig 获取指定用户的 context_token（主动发起会话时调用）
func (a *API) GetConfig(ilinkUserID, contextToken string) (map[string]any, error) {
	return a.post("ilink/bot/getconfig", map[string]any{
		"ilink_user_id": ilinkUserID,
		"context_token": contextToken,
	}, time.Duration(APITimeout)*time.Second)
}

func (a *API) FetchQRCode() (map[string]any, error) {
	u := ensureTrailingSlash(a.BaseURL) + fmt.Sprintf("ilink/bot/get_bot_qrcode?bot_type=%s", BotType)
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func (a *API) PollQRStatus(qrcode string) (map[string]any, error) {
	u := ensureTrailingSlash(a.BaseURL) + fmt.Sprintf("ilink/bot/get_qrcode_status?qrcode=%s", url.QueryEscape(qrcode))
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("iLink-App-ClientVersion", "1")
	c := &http.Client{Timeout: time.Duration(QRPollTimeout) * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return map[string]any{"status": "wait"}, nil
	}
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func (a *API) GetUploadURL(filekey string, mediaType int, toUserID string, rawSize int, rawMD5 string, fileSize int, aesKeyHex string) (map[string]any, error) {
	return a.post("ilink/bot/getuploadurl", map[string]any{
		"filekey": filekey, "media_type": mediaType, "to_user_id": toUserID,
		"rawsize": rawSize, "rawfilemd5": rawMD5, "filesize": fileSize, "aeskey": aesKeyHex, "no_need_thumb": true,
	}, time.Duration(APITimeout)*time.Second)
}

// ── AES-128-ECB ─────────────────────────────────────────────

func aesECBEncrypt(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padLen := 16 - (len(data) % 16)
	padded := make([]byte, len(data)+padLen)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += 16 {
		block.Encrypt(out[i:i+16], padded[i:i+16])
	}
	return out, nil
}

func aesECBDecrypt(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%16 != 0 {
		return nil, fmt.Errorf("ciphertext not multiple of 16")
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += 16 {
		block.Decrypt(out[i:i+16], data[i:i+16])
	}
	// PKCS7 unpad
	if len(out) > 0 {
		padLen := int(out[len(out)-1])
		if padLen > 0 && padLen <= 16 && padLen <= len(out) {
			out = out[:len(out)-padLen]
		}
	}
	return out, nil
}

func aesECBPaddedSize(plainSize int) int {
	return ((plainSize + 1 + 15) / 16) * 16
}

// DownloadMediaFromCDN 从微信 CDN 下载并解密媒体文件，返回本地保存路径。
func DownloadMediaFromCDN(cdnBaseURL, encryptParam, aesKey, savePath string) error {
	dlURL := fmt.Sprintf("%s/download?encrypted_query_param=%s", cdnBaseURL, url.QueryEscape(encryptParam))
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Get(dlURL)
	if err != nil {
		return fmt.Errorf("cdn download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("cdn download status %d", resp.StatusCode)
	}
	ciphertext, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cdn read body: %w", err)
	}

	// 解析 AES key：可能是 32 字符 hex 或 base64 编码
	keyBytes, err := parseAESKey(aesKey)
	if err != nil {
		return fmt.Errorf("parse aes key: %w", err)
	}

	plain, err := aesECBDecrypt(ciphertext, keyBytes)
	if err != nil {
		return fmt.Errorf("aes decrypt: %w", err)
	}

	if dir := filepath.Dir(savePath); dir != "" {
		os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(savePath, plain, 0o644)
}

// parseAESKey 支持三种格式：32 字符 hex → 16 字节；base64 解码后 32 字节 hex → 16 字节；base64 解码后 16 字节直接用。
func parseAESKey(key string) ([]byte, error) {
	if b, err := hex.DecodeString(key); err == nil && len(b) == 16 {
		return b, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("not hex nor base64: %s", key)
	}
	if len(decoded) == 32 {
		if b, err := hex.DecodeString(string(decoded)); err == nil && len(b) == 16 {
			return b, nil
		}
	}
	if len(decoded) == 16 {
		return decoded, nil
	}
	return nil, fmt.Errorf("invalid aes key length %d after decode", len(decoded))
}

// ── CDN Upload ──────────────────────────────────────────────

type UploadResult struct {
	EncryptQueryParam string
	AESKeyB64         string
	CiphertextSize    int
	RawSize           int
}

func UploadMediaToCDN(api *API, filePath, toUserID string, mediaType int) (*UploadResult, error) {
	aesKey := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, aesKey); err != nil {
		return nil, fmt.Errorf("generate aes key: %w", err)
	}
	aesKeyHex := hex.EncodeToString(aesKey)
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	rawSize := len(raw)
	rawMD5 := bytesMD5(raw)
	cipherSize := aesECBPaddedSize(rawSize)
	encrypted, err := aesECBEncrypt(raw, aesKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	var downloadParam string
	var lastErr error
	for attempt := 1; attempt <= UploadMaxRetries; attempt++ {
		filekey := uuid.New().String()
		resp, err := api.GetUploadURL(filekey, mediaType, toUserID, rawSize, rawMD5, cipherSize, aesKeyHex)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		uploadFullURL, _ := resp["upload_full_url"].(string)
		uploadParam, _ := resp["upload_param"].(string)
		uploadURL := uploadFullURL
		if uploadURL == "" && uploadParam != "" {
			uploadURL = fmt.Sprintf("%s/upload?encrypted_query_param=%s&filekey=%s",
				api.CDNBaseURL, url.QueryEscape(uploadParam), url.QueryEscape(filekey))
		}
		if uploadURL == "" {
			lastErr = fmt.Errorf("getUploadUrl returned no URL")
			continue
		}
		cdnReq, _ := http.NewRequest("POST", uploadURL, bytes.NewReader(encrypted))
		cdnReq.Header.Set("Content-Type", "application/octet-stream")
		cdnReq.ContentLength = int64(len(encrypted))
		cdnResp, err := (&http.Client{Timeout: 120 * time.Second}).Do(cdnReq)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		cdnResp.Body.Close()
		if cdnResp.StatusCode >= 400 && cdnResp.StatusCode < 500 {
			lastErr = fmt.Errorf("CDN client error %d", cdnResp.StatusCode)
			break
		}
		downloadParam = cdnResp.Header.Get("x-encrypted-param")
		if downloadParam == "" {
			lastErr = fmt.Errorf("CDN response missing x-encrypted-param")
			continue
		}
		logger.Info("weixin", "", "", fmt.Sprintf("CDN upload ok attempt=%d", attempt), 0)
		break
	}
	if downloadParam == "" {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("CDN upload failed")
	}
	return &UploadResult{
		EncryptQueryParam: downloadParam,
		AESKeyB64:         base64.StdEncoding.EncodeToString([]byte(aesKeyHex)),
		CiphertextSize:    cipherSize,
		RawSize:           rawSize,
	}, nil
}

// ── Helpers ─────────────────────────────────────────────────

func ensureTrailingSlash(u string) string {
	if len(u) > 0 && u[len(u)-1] != '/' {
		return u + "/"
	}
	return u
}

func randomWechatUIN() string {
	val := mrand.Uint32()
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", val)))
}

func bytesMD5(data []byte) string {
	h := md5.Sum(data)
	return hex.EncodeToString(h[:])
}

func resolveMediaPath(pathOrURL string) (string, error) {
	if pathOrURL == "" {
		return "", fmt.Errorf("empty path")
	}
	if p := pathOrURL; len(p) > 7 && p[:7] == "file://" {
		pathOrURL = p[7:]
	}
	// Web 路径（/output/xxx.png）→ 本地绝对路径（{cwd}/output/xxx.png）。
	// 飞书侧采用同样的转换逻辑（filepath.Abs + TrimPrefix "/"）。
	if strings.HasPrefix(pathOrURL, "/") {
		if candidate, err := filepath.Abs(strings.TrimPrefix(pathOrURL, "/")); err == nil {
			if _, statErr := os.Stat(candidate); statErr == nil {
				return candidate, nil
			}
		}
	}
	if len(pathOrURL) > 7 && (pathOrURL[:7] == "http://" || pathOrURL[:8] == "https://") {
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Get(pathOrURL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		ext := ".bin"
		ct := resp.Header.Get("Content-Type")
		for _, pair := range [][2]string{{"jpeg", ".jpg"}, {"png", ".png"}, {"gif", ".gif"}, {"mp4", ".mp4"}, {"pdf", ".pdf"}} {
			if bytes.Contains([]byte(ct), []byte(pair[0])) {
				ext = pair[1]
				break
			}
		}
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("wx_media_%d%s", time.Now().UnixNano(), ext))
		data, _ := io.ReadAll(resp.Body)
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return "", err
		}
		return tmp, nil
	}
	if _, err := os.Stat(pathOrURL); err != nil {
		return "", fmt.Errorf("file not found: %s", pathOrURL)
	}
	return pathOrURL, nil
}
