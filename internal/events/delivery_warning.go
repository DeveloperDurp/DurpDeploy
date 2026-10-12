package events

// SMTPPlaintextStatus is a successful delivery with a transport warning.
const SMTPPlaintextStatus = "ok: sent without TLS"

// SMTPPlaintextWarning reports successful email delivery over plaintext.
// It is the only non-nil Notify result that does not mean delivery failed.
type SMTPPlaintextWarning struct{}

func (SMTPPlaintextWarning) Error() string { return SMTPPlaintextStatus }
