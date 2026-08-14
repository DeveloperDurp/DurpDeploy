package mfa

func (a *WebAuthnAdapter) RPID() string {
	return a.webauthn.Config.RPID
}
