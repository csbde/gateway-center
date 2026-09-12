// Package cryptox 提供应用层 AES-256-GCM 加解密与指纹（T012，research R7、宪法 VIII）。
// 加密对象：DNS Provider 凭证、导入证书私钥、Traefik API Basic 凭据。
package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

var (
	ErrKeyLength  = errors.New("主密钥必须为 32 字节")
	ErrCiphertext = errors.New("密文不合法或密钥不匹配")
)

// Cipher 绑定一把 32 字节主密钥。
type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, ErrKeyLength
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 输出 nonce||ciphertext（随机 nonce，每次不同）。
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *Cipher) Decrypt(data []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(data) < ns+16 {
		return nil, ErrCiphertext
	}
	plain, err := c.aead.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return nil, ErrCiphertext
	}
	return plain, nil
}

// Fingerprint 返回明文 SHA-256 前 4 字节的 hex（8 字符），供轮换确认（FR-035）。
func Fingerprint(plaintext []byte) string {
	sum := sha256.Sum256(plaintext)
	return hex.EncodeToString(sum[:4])
}
