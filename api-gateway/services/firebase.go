package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

var (
	FirebaseApp *firebase.App
	FCMClient   *messaging.Client
)

// InitFirebase initializes the Firebase Admin Go SDK for Bytfin backend services.
func InitFirebase() error {
	ctx := context.Background()

	// Locate credentials file from environment or standard config paths
	credPath := os.Getenv("FIREBASE_CREDENTIALS_FILE")
	if credPath == "" {
		credPath = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	}

	searchPaths := []string{
		credPath,
		"config/bytfin-blockchain.json",
		"bytfin-blockchain.json",
		"../config/bytfin-blockchain.json",
		"../bytfin-blockchain.json",
		"/Volumes/Untitled/zwc/identity/api-gateway/config/bytfin-blockchain.json",
		"/Volumes/Untitled/zwc/bytfin-blockchain.json",
	}

	var validPath string
	for _, p := range searchPaths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if absPath, err := filepath.Abs(p); err == nil {
			if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
				validPath = absPath
				break
			}
		}
	}

	var opt option.ClientOption
	if validPath != "" {
		log.Printf("[FIREBASE-ADMIN] Loading credentials from: %s", validPath)
		opt = option.WithCredentialsFile(validPath)
	} else {
		log.Println("[FIREBASE-ADMIN] Warning: No service account JSON file found. FCM will run in dev/mock mode.")
		return nil
	}

	config := &firebase.Config{
		ProjectID: "bytfin-blockchain",
	}

	app, err := firebase.NewApp(ctx, config, opt)
	if err != nil {
		return fmt.Errorf("failed to initialize firebase app: %w", err)
	}
	FirebaseApp = app

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize firebase messaging client: %w", err)
	}
	FCMClient = client

	log.Println("[FIREBASE-ADMIN] Firebase Admin SDK & FCM Messaging client initialized successfully (Project: bytfin-blockchain)")
	return nil
}

// SendFCMPush dispatches an FCM push notification to a single device token.
// Returns (bool invalidToken, error err)
func SendFCMPush(ctx context.Context, token, title, body string, data map[string]string) (bool, error) {
	if FCMClient == nil {
		log.Printf("[FIREBASE-MOCK] SendFCMPush (No live FCMClient) -> Token=%s | Title=%q | Body=%q", token, title, body)
		return false, nil
	}

	if strings.TrimSpace(token) == "" {
		return true, fmt.Errorf("empty device token")
	}

	if data == nil {
		data = make(map[string]string)
	}

	msg := &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
		Android: &messaging.AndroidConfig{
			Priority: "high",
			Notification: &messaging.AndroidNotification{
				ChannelID:     "bytfin_transfers_channel",
				Sound:         "default",
				DefaultSound:  true,
				ClickAction:   "FLUTTER_NOTIFICATION_CLICK",
			},
		},
		APNS: &messaging.APNSConfig{
			Headers: map[string]string{
				"apns-priority": "10",
			},
			Payload: &messaging.APNSPayload{
				Aps: &messaging.Aps{
					Sound:            "default",
					ContentAvailable: true,
				},
			},
		},
	}

	response, err := FCMClient.Send(ctx, msg)
	if err != nil {
		isInvalid := messaging.IsUnregistered(err) || messaging.IsInvalidArgument(err)
		log.Printf("[FIREBASE-FCM-ERROR] Failed to send push to token %s (invalid=%v): %v", token, isInvalid, err)
		return isInvalid, err
	}

	log.Printf("[FIREBASE-FCM-SUCCESS] Push dispatched successfully (MessageID: %s) -> Token: %s", response, token)
	return false, nil
}

// SendFCMMulticast sends a push notification to multiple device tokens simultaneously.
// Returns a slice of invalid tokens that should be pruned from the database.
func SendFCMMulticast(ctx context.Context, tokens []string, title, body string, data map[string]string) ([]string, error) {
	if len(tokens) == 0 {
		return nil, nil
	}

	if FCMClient == nil {
		log.Printf("[FIREBASE-MOCK] SendFCMMulticast (%d tokens) -> Title=%q | Body=%q", len(tokens), title, body)
		return nil, nil
	}

	if data == nil {
		data = make(map[string]string)
	}

	multicastMsg := &messaging.MulticastMessage{
		Tokens: tokens,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
		Android: &messaging.AndroidConfig{
			Priority: "high",
			Notification: &messaging.AndroidNotification{
				ChannelID:    "bytfin_transfers_channel",
				Sound:        "default",
				DefaultSound: true,
			},
		},
		APNS: &messaging.APNSConfig{
			Payload: &messaging.APNSPayload{
				Aps: &messaging.Aps{
					Sound:            "default",
					ContentAvailable: true,
				},
			},
		},
	}

	br, err := FCMClient.SendEachForMulticast(ctx, multicastMsg)
	if err != nil {
		return nil, fmt.Errorf("multicast send failed: %w", err)
	}

	var invalidTokens []string
	for idx, resp := range br.Responses {
		if !resp.Success {
			if messaging.IsUnregistered(resp.Error) || messaging.IsInvalidArgument(resp.Error) {
				invalidTokens = append(invalidTokens, tokens[idx])
			}
			log.Printf("[FIREBASE-MULTICAST-WARN] Token failed: %s -> %v", tokens[idx], resp.Error)
		}
	}

	log.Printf("[FIREBASE-MULTICAST-SUCCESS] Multicast sent: %d succeeded, %d failed", br.SuccessCount, br.FailureCount)
	return invalidTokens, nil
}
