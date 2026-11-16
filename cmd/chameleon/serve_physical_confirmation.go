package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type physicalPlanReceipt struct {
	Schema  int    `json:"schema"`
	Kind    string `json:"kind"`
	Session string `json:"session"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func (ws *webServer) physicalConfirmation(kind string, payload any) (string, error) {
	ws.physicalOnce.Do(func() { _, ws.physicalKeyError = rand.Read(ws.physicalKey[:]) })
	if ws.physicalKeyError != nil {
		return "", ws.physicalKeyError
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digestMAC := hmac.New(sha256.New, ws.physicalKey[:])
	digestMAC.Write([]byte("payload\x00"))
	digestMAC.Write(raw)
	digest := hex.EncodeToString(digestMAC.Sum(nil))
	session := ws.currentSessionID()
	if session == "" {
		return "", fmt.Errorf("device session required for a physical-write plan")
	}
	receipt := physicalPlanReceipt{1, kind, session, digest, time.Now().Add(5 * time.Minute).Unix()}
	data, _ := json.Marshal(receipt)
	mac := hmac.New(sha256.New, ws.physicalKey[:])
	mac.Write([]byte("receipt\x00"))
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (ws *webServer) verifyPhysicalConfirmation(kind string, payload any, token string) error {
	if len(token) > 2048 {
		return fmt.Errorf("invalid physical-write confirmation")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return fmt.Errorf("review a physical-write plan before execution")
	}
	data, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return fmt.Errorf("invalid physical-write confirmation")
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return fmt.Errorf("invalid physical-write confirmation")
	}
	ws.physicalOnce.Do(func() { _, ws.physicalKeyError = rand.Read(ws.physicalKey[:]) })
	if ws.physicalKeyError != nil {
		return ws.physicalKeyError
	}
	mac := hmac.New(sha256.New, ws.physicalKey[:])
	mac.Write([]byte("receipt\x00"))
	mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return fmt.Errorf("physical-write plan confirmation is invalid or from another server")
	}
	var receipt physicalPlanReceipt
	if json.Unmarshal(data, &receipt) != nil {
		return fmt.Errorf("invalid physical-write confirmation")
	}
	raw, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	digestMAC := hmac.New(sha256.New, ws.physicalKey[:])
	digestMAC.Write([]byte("payload\x00"))
	digestMAC.Write(raw)
	digest := hex.EncodeToString(digestMAC.Sum(nil))
	if receipt.Schema != 1 || receipt.Kind != kind || receipt.Session != ws.currentSessionID() || !hmac.Equal([]byte(receipt.Digest), []byte(digest)) || receipt.Expires < time.Now().Unix() {
		return fmt.Errorf("physical-write plan expired or its session/input changed; review a new plan")
	}
	return nil
}
