package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type Token struct {
	Header    map[string]interface{}
	Payload   map[string]interface{}
	Signature string
}

func EncodeJWT(claims map[string]interface{}, secretKey string) (string, error) {
	// Header
	header := map[string]interface{}{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerBase64 := base64.URLEncoding.EncodeToString(headerJSON)

	// Payload
	claims["exp"] = time.Now().Add(time.Hour * 24).Unix()
	payloadJSON, _ := json.Marshal(claims)
	payloadBase64 := base64.URLEncoding.EncodeToString(payloadJSON)

	// Signature
	signatureInput := headerBase64 + "." + payloadBase64
	signature := generateSignature(signatureInput, secretKey)

	return signatureInput + "." + signature, nil
}

func DecodeJWT(tokenString string, secretKey string) (bool, map[string]interface{}) {
	return DecodeJWTInto(tokenString, secretKey, nil)
}

// DecodeJWTInto is DecodeJWT that also decodes a valid token's payload into
// target (e.g. a struct), straight from the token instead of re-encoding the
// map. target may be nil.
func DecodeJWTInto(tokenString string, secretKey string, target interface{}) (bool, map[string]interface{}) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return false, nil
	}

	headerBase64 := parts[0]
	payloadBase64 := parts[1]
	signatureProvided := parts[2]

	// Verify signature
	signatureInput := headerBase64 + "." + payloadBase64
	expectedSignature := generateSignature(signatureInput, secretKey)
	// Constant-time comparison, so response timing doesn't reveal how much of
	// a forged signature is right.
	if !hmac.Equal([]byte(signatureProvided), []byte(expectedSignature)) {
		return false, nil
	}

	// Decode payload
	payloadJSON, err := base64.URLEncoding.DecodeString(payloadBase64)
	if err != nil {
		return false, nil
	}

	var payload map[string]interface{}
	json.Unmarshal(payloadJSON, &payload)

	// Check expiration
	if exp, ok := payload["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return false, nil
		}
	}

	if target != nil {
		json.Unmarshal(payloadJSON, target)
	}

	return true, payload
}

func generateSignature(input string, secretKey string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(input))
	return base64.URLEncoding.EncodeToString(mac.Sum(nil))
}
