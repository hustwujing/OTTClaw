// internal/tool/weixin.go — 微信个人号工具处理器
package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
	"OTTClaw/internal/weixin"
)

func handleWeixin(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse weixin action: %w", err)
	}
	switch base.Action {
	case "bind":
		return handleWeixinBind(ctx)
	case "status":
		return handleWeixinStatus(ctx)
	case "unbind":
		return handleWeixinUnbind(ctx)
	case "send":
		return handleWeixinSend(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown weixin action: %q (valid: bind/status/unbind/send)", base.Action)
	}
}

func handleWeixinBind(ctx context.Context) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	client := weixin.GetActiveClient(userID)
	if client != nil && client.LoginStatus == "logged_in" {
		resp := map[string]any{"status": "already_bound", "message": "微信已绑定且在线，如需重新绑定请先调用 unbind"}
		if cfg, _ := storage.GetWeixinConfig(userID); cfg != nil {
			ownerID := cfg.OwnerIlinkUserID
			if ownerID == "" {
				if senders := client.GetKnownSenders(); len(senders) > 0 {
					ownerID = senders[0]
				}
			}
			resp["owner_weixin_id"] = ownerID
		}
		b, _ := json.Marshal(resp)
		return string(b), nil
	}
	if client != nil && (client.LoginStatus == "waiting_scan" || client.LoginStatus == "scanned") {
		result := map[string]any{"status": client.LoginStatus, "message": "绑定流程进行中"}
		if client.CurrentQRURL != "" {
			result["qr_url"] = client.CurrentQRURL
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
	reg := weixin.GetRegistry()
	if reg == nil {
		return "", fmt.Errorf("weixin registry not initialized")
	}

	bindClient := weixin.NewClient(userID, nil)
	weixin.SetActiveClientForBind(userID, bindClient)

	go func() {
		result, err := bindClient.QRLogin(context.Background(), weixin.DefaultBaseURL)
		if err != nil {
			logger.Warn("weixin", userID, "", fmt.Sprintf("bind QR login failed: %v", err), 0)
			return
		}
		if result == nil {
			return
		}
		token, _ := result["token"].(string)
		baseURL, _ := result["base_url"].(string)
		botID, _ := result["bot_id"].(string)
		ilinkUserID, _ := result["ilink_user_id"].(string)
		if err := storage.SetWeixinConfig(userID, token, baseURL, botID, ilinkUserID); err != nil {
			logger.Warn("weixin", userID, "", fmt.Sprintf("save weixin config: %v", err), 0)
			bindClient.LoginStatus = "bind_failed"
			bindClient.BindError = err.Error()
			return
		}
		logger.Info("weixin", userID, "", "weixin bound successfully, starting connection", 0)
		reg.StartForUser(context.Background(), userID)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bindClient.CurrentQRURL != "" {
			qrURL := bindClient.CurrentQRURL
			qrData := map[string]any{"url": qrURL, "message": "请使用微信扫描二维码完成绑定（二维码约2分钟后过期）"}
			if png, err := qrcode.Encode(qrURL, qrcode.Medium, 256); err == nil {
				qrData["image"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			}
			if sender := interactiveSenderFromCtx(ctx); sender != nil {
				_ = sender("qrcode", qrData)
			}
			b, _ := json.Marshal(map[string]any{
				"status":  "waiting_scan",
				"message": "二维码已展示给用户，等待扫码中。扫码后在手机上确认即可完成绑定。可通过 status 查询绑定结果。注意：二维码已通过交互控件展示，请勿再用 markdown 图片语法重复展示。",
			})
			return string(b), nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	b, _ := json.Marshal(map[string]any{"status": "pending", "message": "正在获取二维码，请稍后通过 status 查询..."})
	return string(b), nil
}

func handleWeixinStatus(ctx context.Context) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	cfg, err := storage.GetWeixinConfig(userID)
	if err != nil {
		return "", err
	}
	result := map[string]any{"bound": false, "login_status": "idle", "bot_id": "", "owner_weixin_id": ""}
	if cfg != nil && cfg.TokenEnc != "" {
		result["bound"] = true
		result["bot_id"] = cfg.BotID
		result["owner_weixin_id"] = cfg.OwnerIlinkUserID // 绑定者自己的微信 ID，send 时发给自己用此值
	}
	client := weixin.GetActiveClient(userID)
	if client != nil {
		result["login_status"] = client.LoginStatus
		if client.CurrentQRURL != "" {
			result["qr_url"] = client.CurrentQRURL
		}
		if client.BindError != "" {
			result["bind_error"] = client.BindError
		}
		// DB 中 owner_weixin_id 为空时，从内存缓存兜底（兼容旧数据）
		if result["owner_weixin_id"] == "" {
			if senders := client.GetKnownSenders(); len(senders) > 0 {
				result["owner_weixin_id"] = senders[0]
			}
		}
	} else if cfg != nil && cfg.TokenEnc != "" {
		result["login_status"] = "disconnected"
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func handleWeixinUnbind(ctx context.Context) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	if reg := weixin.GetRegistry(); reg != nil {
		reg.StopForUser(userID)
	}
	if err := storage.DeleteWeixinConfig(userID); err != nil {
		return "", fmt.Errorf("delete weixin config: %w", err)
	}
	b, _ := json.Marshal(map[string]any{"status": "ok", "message": "微信已解绑"})
	return string(b), nil
}

func handleWeixinSend(ctx context.Context, argsJSON string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	var args struct {
		To   string `json:"to"`
		Text string `json:"text"`
		File string `json:"file"` // 本地文件路径或 URL（图片/文件，优先于 text）
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if args.To == "" {
		return "", fmt.Errorf("to is required")
	}
	if args.Text == "" && args.File == "" {
		return "", fmt.Errorf("text or file is required")
	}
	client := weixin.GetActiveClient(userID)
	if client == nil || client.LoginStatus != "logged_in" {
		return "", fmt.Errorf("微信未连接，请先绑定微信")
	}
	// 发送文件/图片
	if args.File != "" {
		var sendErr error
		fileType := "file"
		if isImagePath(args.File) {
			sendErr = client.SendImage(args.To, args.File, "")
			fileType = "image"
		} else {
			sendErr = client.SendFile(args.To, args.File, "")
		}
		if sendErr != nil {
			return "", fmt.Errorf("发送%s失败: %w", fileType, sendErr)
		}
		if args.Text != "" {
			_ = client.SendText(args.To, args.Text, "")
		}
		b, _ := json.Marshal(map[string]any{"status": "ok", "type": fileType})
		return string(b), nil
	}
	// 发送文本
	if err := client.SendText(args.To, args.Text, ""); err != nil {
		return "", fmt.Errorf("发送失败: %w", err)
	}
	b, _ := json.Marshal(map[string]any{"status": "ok", "type": "text"})
	return string(b), nil
}

