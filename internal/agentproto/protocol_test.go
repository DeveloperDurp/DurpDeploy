package agentproto

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProtocolVersion_Parse_accepts_agent_v1(t *testing.T) {
	// Given
	raw := "agent/1"

	// When
	version, err := ParseProtocolVersion(raw)

	// Then
	if err != nil {
		t.Fatalf("ParseProtocolVersion(%q): %v", raw, err)
	}
	if version != AgentV1 {
		t.Fatalf("version = %q, want %q", version, AgentV1)
	}
}

func TestProtocolVersion_Parse_rejects_agent_v2(t *testing.T) {
	// Given
	raw := "agent/2"

	// When
	_, err := ParseProtocolVersion(raw)

	// Then
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf(
			"ParseProtocolVersion(%q) error = %v, want %v",
			raw,
			err,
			ErrUnsupportedProtocol,
		)
	}
}

func TestDecodeRequest_rejects_malformed_or_extra_json(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{
			name:    "unknown field",
			body:    `{"protocol":"agent/1","agent_version":"v1","extra":true}`,
			wantErr: ErrUnknownField,
		},
		{
			name:    "trailing JSON value",
			body:    `{"protocol":"agent/1","agent_version":"v1"} {}`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "unsupported protocol",
			body:    `{"protocol":"agent/2","agent_version":"v1"}`,
			wantErr: ErrUnsupportedProtocol,
		},
		{
			name:    "omitted protocol",
			body:    `{"agent_version":"v1"}`,
			wantErr: ErrUnsupportedProtocol,
		},
		{
			name:    "null protocol",
			body:    `{"protocol":null,"agent_version":"v1"}`,
			wantErr: ErrUnsupportedProtocol,
		},
		{
			name:    "blank protocol",
			body:    `{"protocol":"","agent_version":"v1"}`,
			wantErr: ErrUnsupportedProtocol,
		},
		{
			name:    "malformed JSON",
			body:    `{"protocol":"agent/1",`,
			wantErr: ErrInvalidJSON,
		},
		{
			name: "request larger than one mebibyte",
			body: `{"protocol":"agent/1","agent_version":"` +
				strings.Repeat("x", MaxRequestBytes) + `"}`,
			wantErr: ErrRequestTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			body := strings.NewReader(test.body)

			// When
			_, err := DecodeRequest[PollRequest](body)

			// Then
			if !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"DecodeRequest() error = %v, want %v",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestDecodeRequest_rejects_omitted_protocol_for_every_request(
	t *testing.T,
) {
	tests := []struct {
		name   string
		decode func() error
	}{
		{
			name: "poll",
			decode: func() error {
				_, err := DecodeRequest[PollRequest](strings.NewReader(`{}`))
				return err
			},
		},
		{
			name: "start",
			decode: func() error {
				_, err := DecodeRequest[StartRequest](strings.NewReader(`{}`))
				return err
			},
		},
		{
			name: "heartbeat",
			decode: func() error {
				_, err := DecodeRequest[HeartbeatRequest](
					strings.NewReader(`{}`),
				)
				return err
			},
		},
		{
			name: "logs",
			decode: func() error {
				_, err := DecodeLogBatch(strings.NewReader(`{}`))
				return err
			},
		},
		{
			name: "result",
			decode: func() error {
				_, err := DecodeRequest[ResultRequest](strings.NewReader(`{}`))
				return err
			},
		},
		{
			name: "cancelled",
			decode: func() error {
				_, err := DecodeRequest[CancelledRequest](
					strings.NewReader(`{}`),
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			decode := test.decode

			// When
			err := decode()

			// Then
			if !errors.Is(err, ErrUnsupportedProtocol) {
				t.Fatalf(
					"decode() error = %v, want %v",
					err,
					ErrUnsupportedProtocol,
				)
			}
		})
	}
}

func TestDecodeRequest_decodes_agent_v1_payload(t *testing.T) {
	// Given
	body := strings.NewReader(`{"protocol":"agent/1","agent_version":"v1"}`)

	// When
	request, err := DecodeRequest[PollRequest](body)

	// Then
	if err != nil {
		t.Fatalf("DecodeRequest(): %v", err)
	}
	if request.Protocol != AgentV1 || request.AgentVersion != "v1" {
		t.Fatalf("request = %#v, want agent/1 and v1", request)
	}
}

func TestDecodeLogBatch_enforces_batch_and_line_limits(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{
			name:    "unknown event field",
			body:    `{"protocol":"agent/1","claim_token":"","events":[{"sequence":1,"line":"ok","extra":true}]}`,
			wantErr: ErrUnknownField,
		},
		{
			name:    "more than one hundred events",
			body:    logBatchBody(101, "ok"),
			wantErr: ErrLogEventLimit,
		},
		{
			name:    "line larger than sixteen kibibytes",
			body:    logBatchBody(1, strings.Repeat("x", MaxLogLineBytes+1)),
			wantErr: ErrLogLineTooLarge,
		},
		{
			name:    "line with invalid UTF-8",
			body:    logBatchBody(1, string([]byte{0xff})),
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "batch larger than 256 kibibytes",
			body:    logBatchBody(17, strings.Repeat("x", MaxLogLineBytes)),
			wantErr: ErrLogBatchTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			body := strings.NewReader(test.body)

			// When
			_, err := DecodeLogBatch(body)

			// Then
			if !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"DecodeLogBatch() error = %v, want %v",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestDecodeLogBatch_accepts_valid_UTF8_line(t *testing.T) {
	// Given
	line := "deployment complete ✓"
	body := strings.NewReader(logBatchBody(1, line))

	// When
	batch, err := DecodeLogBatch(body)

	// Then
	if err != nil {
		t.Fatalf("DecodeLogBatch(): %v", err)
	}
	if len(batch.Events) != 1 || batch.Events[0].Line != line {
		t.Fatalf("batch events = %#v, want one valid UTF-8 line", batch.Events)
	}
}

func TestLimits_are_fixed(t *testing.T) {
	// Given
	// When
	// Then
	if MaxRequestBytes != 1<<20 || MaxLogEvents != 100 ||
		MaxLogBatchBytes != 256<<10 || MaxLogLineBytes != 16<<10 ||
		PollInterval != 25*time.Second || HeartbeatInterval != 10*time.Second ||
		PreStartClaimTimeout != 60*time.Second || LostThreshold != 45*time.Second ||
		CancelAcknowledgementTimeout != 30*time.Second {
		t.Fatal("agent protocol limits changed")
	}
}

func logBatchBody(count int, line string) string {
	events := make([]string, count)
	for index := range events {
		events[index] = `{"sequence":` + strconv.Itoa(
			index+1,
		) + `,"line":"` + line + `"}`
	}
	return `{"protocol":"agent/1","claim_token":"","events":[` + strings.Join(
		events,
		",",
	) + `]}`
}
