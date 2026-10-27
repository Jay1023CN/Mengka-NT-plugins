// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package distribution

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

var header = []byte{'C', 'T', 'H', 'D', 1}

func aead() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(FeedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("更新数据密钥无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func Seal(kind string, plain []byte) ([]byte, error) {
	box, err := aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, box.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte(nil), header...)
	out = append(out, nonce...)
	return box.Seal(out, nonce, plain, []byte("catherina/"+kind+"/v1")), nil
}
func Open(kind string, raw []byte) ([]byte, error) {
	box, err := aead()
	if err != nil {
		return nil, err
	}
	n := len(header)
	if len(raw) < n+box.NonceSize()+box.Overhead() || !bytes.Equal(raw[:n], header) {
		return nil, errors.New("更新数据格式无效")
	}
	plain, err := box.Open(nil, raw[n:n+box.NonceSize()], raw[n+box.NonceSize():], []byte("catherina/"+kind+"/v1"))
	if err != nil {
		return nil, errors.New("更新数据校验失败")
	}
	return plain, nil
}
