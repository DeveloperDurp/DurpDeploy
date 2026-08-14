package mfa

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
)

type webauthnRegistrationVector struct {
	body         []byte
	challenge    string
	credentialID []byte
}

type webauthnAssertionVector struct {
	body         []byte
	parsed       *protocol.ParsedCredentialAssertionData
	challenge    string
	credentialID []byte
	publicKey    []byte
}

func webauthnRegistrationNoneES256(t *testing.T) webauthnRegistrationVector {
	t.Helper()

	const (
		attestationObjectHex = "a363666d74646e6f6e656761747453746d74a068617574684461746158a4bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b559000000008446ccb9ab1db374750b2367ff6f3a1f0020f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4a5010203262001215820afefa16f97ca9b2d23eb86ccb64098d20db90856062eb249c33a9b672f26df61225820930a56b87a2fca66334b03458abf879717c12cc68ed73290af2e2664796b9220"
		clientDataJSONHex    = "7b2274797065223a22776562617574686e2e637265617465222c226368616c6c656e6765223a22414d4d507434557878475453746e63647134313759447742466938767049612d7077386f4f755657345441222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73652c22657874726144617461223a22636c69656e74446174614a534f4e206d617920626520657874656e6465642077697468206164646974696f6e616c206669656c647320696e20746865206675747572652c207375636820617320746869733a20426b5165446a646354427258426941774a544c453551227d"
		credentialIDHex      = "f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4"
		challengeHex         = "00c30fb78531c464d2b6771dab8d7b603c01162f2fa486bea70f283ae556e130"
	)

	credentialID := webauthnDecodeHex(t, credentialIDHex)
	body := webauthnCredentialBody(t, credentialID, map[string]string{
		"attestationObject": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, attestationObjectHex),
		),
		"clientDataJSON": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, clientDataJSONHex),
		),
	})

	return webauthnRegistrationVector{
		body: body,
		challenge: base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, challengeHex),
		),
		credentialID: credentialID,
	}
}

func webauthnRegistrationPackedSelfES256(
	t *testing.T,
) webauthnRegistrationVector {
	t.Helper()

	const (
		attestationObjectHex = "a363666d74667061636b65646761747453746d74a263616c672663736967584630440220067a20754ab925005dbf378097c92120031581c73228d1fb4f5b881bcd7da98302207fc7b147558c7c0eba3af18bd9d121fa3d3a26d17fe3f220272178f473b6006d68617574684461746158a4bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b55d00000000df850e09db6afbdfab51697791506cfc0020455ef34e2043a87db3d4afeb39bbcb6cc32df9347c789a865ecdca129cbef58ca5010203262001215820eb151c8176b225cc651559fecf07af450fd85802046656b34c18f6cf193843c5225820927b8aa427a2be1b8834d233a2d34f61f13bfd44119c325d5896e183fee484f2"
		clientDataJSONHex    = "7b2274797065223a22776562617574686e2e637265617465222c226368616c6c656e6765223a2265476e4374334c55745936366b336a506a796e6962506b31716e666644616966715a774c33417032392d55222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73652c22657874726144617461223a22636c69656e74446174614a534f4e206d617920626520657874656e6465642077697468206164646974696f6e616c206669656c647320696e20746865206675747572652c207375636820617320746869733a205539685458764b453255526b4d6e625f307859485667227d"
		credentialIDHex      = "455ef34e2043a87db3d4afeb39bbcb6cc32df9347c789a865ecdca129cbef58c"
		challengeHex         = "7869c2b772d4b58eba9378cf8f29e26cf935aa77df0da89fa99c0bdc0a76f7e5"
	)

	credentialID := webauthnDecodeHex(t, credentialIDHex)
	body := webauthnCredentialBody(t, credentialID, map[string]string{
		"attestationObject": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, attestationObjectHex),
		),
		"clientDataJSON": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, clientDataJSONHex),
		),
	})

	return webauthnRegistrationVector{
		body: body,
		challenge: base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, challengeHex),
		),
		credentialID: credentialID,
	}
}

func webauthnAssertionNoneES256(t *testing.T) webauthnAssertionVector {
	t.Helper()

	const (
		authenticatorDataHex = "bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b51900000000"
		clientDataJSONHex    = "7b2274797065223a22776562617574686e2e676574222c226368616c6c656e6765223a224f63446e55685158756c5455506f334a5558543049393770767a7a59425039745a63685879617630314167222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73657d"
		signatureHex         = "3046022100f50a4e2e4409249c4a853ba361282f09841df4dd4547a13a87780218deffcd380221008480ac0f0b93538174f575bf11a1dd5d78c6e486013f937295ea13653e331e87"
		credentialIDHex      = "f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4"
		challengeHex         = "39c0e7521417ba54d43e8dc95174f423dee9bf3cd804ff6d65c857c9abf4d408"
		credentialPubKeyHex  = "a5010203262001215820afefa16f97ca9b2d23eb86ccb64098d20db90856062eb249c33a9b672f26df61225820930a56b87a2fca66334b03458abf879717c12cc68ed73290af2e2664796b9220"
	)

	credentialID := webauthnDecodeHex(t, credentialIDHex)
	body := webauthnCredentialBody(t, credentialID, map[string]string{
		"authenticatorData": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, authenticatorDataHex),
		),
		"clientDataJSON": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, clientDataJSONHex),
		),
		"signature": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, signatureHex),
		),
	})
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		t.Fatalf("parse upstream assertion vector: %v", err)
	}

	return webauthnAssertionVector{
		body:   body,
		parsed: parsed,
		challenge: base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, challengeHex),
		),
		credentialID: credentialID,
		publicKey:    webauthnDecodeHex(t, credentialPubKeyHex),
	}
}

func webauthnAssertionNoneES256LongCredentialID(
	t *testing.T,
) webauthnAssertionVector {
	t.Helper()

	const (
		authenticatorDataHex = "bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b50d00000000"
		clientDataJSONHex    = "7b2274797065223a22776562617574686e2e676574222c226368616c6c656e6765223a22377833727057334f53505a307045664d396a75566d53574d36485a4935634f573875384d6f647047446a73222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73657d"
		signatureHex         = "304502203ecef83fb12a0cae7841055f9f87103a99fd14b424194bbf06c4623d3ee6e3fd022100d2ace346db262b1374a6b70faa51f518a42ddca13a4125ce6f5052a75bac9fb6"
		challengeHex         = "ef1deba56dce48f674a447ccf63b9599258ce87648e5c396f2ef0ca1da460e3b"
		credentialPubKeyHex  = "a50102032620012158203b8176b7504489cc593046d7988abb7905a742de6ac2cdc748a873c663e90cb12258201436d5edc9a75f23999eef9d5950a5c2455514ee1014084720f841a06b828a11"
	)

	credentialID := webauthnOfficialLongCredentialID(t)
	body := webauthnCredentialBody(t, credentialID, map[string]string{
		"authenticatorData": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, authenticatorDataHex),
		),
		"clientDataJSON": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, clientDataJSONHex),
		),
		"signature": base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, signatureHex),
		),
	})
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		t.Fatalf("parse upstream UV assertion vector: %v", err)
	}

	return webauthnAssertionVector{
		body:   body,
		parsed: parsed,
		challenge: base64.RawURLEncoding.EncodeToString(
			webauthnDecodeHex(t, challengeHex),
		),
		credentialID: credentialID,
		publicKey:    webauthnDecodeHex(t, credentialPubKeyHex),
	}
}

func webauthnOfficialLongCredentialID(t *testing.T) []byte {
	t.Helper()

	const (
		caseMarker  = "name:                        \"NoneES256LongCredentialID\""
		fieldMarker = "credentialID:                \""
	)

	source, err := os.ReadFile(webauthnOfficialVectorSourcePath(t))
	if err != nil {
		t.Fatalf("read official WebAuthn vector: %v", err)
	}
	caseOffset := bytes.Index(source, []byte(caseMarker))
	if caseOffset < 0 {
		t.Fatal("official WebAuthn vector is unavailable")
	}
	fieldOffset := bytes.Index(source[caseOffset:], []byte(fieldMarker))
	if fieldOffset < 0 {
		t.Fatal("official WebAuthn credential ID is unavailable")
	}
	valueStart := caseOffset + fieldOffset + len(fieldMarker)
	valueEnd := bytes.IndexByte(source[valueStart:], '"')
	if valueEnd < 0 {
		t.Fatal("official WebAuthn credential ID is malformed")
	}
	return webauthnDecodeHex(t, string(source[valueStart:valueStart+valueEnd]))
}

func webauthnOfficialVectorSourcePath(t *testing.T) string {
	t.Helper()

	return filepath.Join(
		webauthnModuleCache(t),
		"github.com/go-webauthn/webauthn@v0.17.4/protocol/specification_vectors_e2e_test.go",
	)
}

func webauthnModuleCache(t *testing.T) string {
	t.Helper()

	if cache := os.Getenv("GOMODCACHE"); cache != "" {
		return cache
	}
	output, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("resolve GOMODCACHE: %v", err)
	}
	cache := strings.TrimSpace(string(output))
	if cache == "" {
		t.Fatal("GOMODCACHE is empty")
	}
	return cache
}

type webauthnAssertionResponseFields struct {
	credentialID      []byte
	userHandle        []byte
	authenticatorData []byte
}

func webauthnAssertionBody(
	t *testing.T,
	vector webauthnAssertionVector,
	fields webauthnAssertionResponseFields,
) []byte {
	t.Helper()

	authenticatorData := vector.parsed.Raw.AssertionResponse.AuthenticatorData
	if len(fields.authenticatorData) != 0 {
		authenticatorData = fields.authenticatorData
	}
	response := map[string]string{
		"authenticatorData": base64.RawURLEncoding.EncodeToString(
			authenticatorData,
		),
		"clientDataJSON": base64.RawURLEncoding.EncodeToString(
			vector.parsed.Raw.AssertionResponse.ClientDataJSON,
		),
		"signature": base64.RawURLEncoding.EncodeToString(
			vector.parsed.Raw.AssertionResponse.Signature,
		),
	}
	if len(fields.userHandle) != 0 {
		response["userHandle"] = base64.RawURLEncoding.EncodeToString(
			fields.userHandle,
		)
	}
	return webauthnCredentialBody(t, fields.credentialID, response)
}

func webauthnCredentialBody(
	t *testing.T,
	credentialID []byte,
	response map[string]string,
) []byte {
	t.Helper()

	encodedID := base64.RawURLEncoding.EncodeToString(credentialID)
	body, err := json.Marshal(map[string]any{
		"id":       encodedID,
		"rawId":    encodedID,
		"type":     "public-key",
		"response": response,
	})
	if err != nil {
		t.Fatalf("marshal upstream vector: %v", err)
	}
	return body
}

func webauthnDecodeHex(t *testing.T, value string) []byte {
	t.Helper()

	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode upstream vector: %v", err)
	}
	return decoded
}
