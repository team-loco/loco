package transit

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

func TestEncryptionKeyNamesEachOperationSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	key := testKey(newTestClient(t, transitConfig(transittest.New(t).Server().URL)))
	ctx := tracingtest.StartParent(t)
	keyID, ciphertext, err := key.Encrypt(ctx, testPlaintext(t), testContext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := key.Decrypt(ctx, keyID, ciphertext, testContext); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	named := attribute.String(keyAttr, transittest.Key)
	encrypt := tracingtest.Operation(t, recorder, encryptSpan, named)
	readKey := tracingtest.Operation(t, recorder, readKeySpan, named)
	if readKey.Parent().SpanID() != encrypt.SpanContext().SpanID() {
		t.Fatalf("%s is not a child of %s", readKeySpan, encryptSpan)
	}
	tracingtest.Operation(t, recorder, decryptSpan, named)
}

func TestSignerNamesEachOperationSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	_, signer := newTestSigner(t)
	ctx := tracingtest.StartParent(t)
	if _, err := signer.PublicKeys(ctx); err != nil {
		t.Fatalf("public keys: %v", err)
	}
	named := attribute.String(keyAttr, transittest.SigningKey)
	tracingtest.Operation(t, recorder, readKeySpan, named)
	if _, err := signer.Sign(ctx, "v1", []byte(testSigningInput)); err != nil {
		t.Fatalf("sign: %v", err)
	}
	tracingtest.Operation(t, recorder, signSpan, named)
}

func TestOperationSpanRecordsAFailure(t *testing.T) {
	recorder := tracingtest.Record(t)
	fake, signer := newTestSigner(t)
	fake.SetSigningKeyType(encryptionKeyType)
	if _, err := signer.KeyID(tracingtest.StartParent(t)); err == nil {
		t.Fatal("key id of an encryption key succeeded")
	}
	span := tracingtest.Operation(t, recorder, readKeySpan)
	if span.Status().Code != codes.Error {
		t.Fatalf("%s status = %+v, want an error", readKeySpan, span.Status())
	}
}
