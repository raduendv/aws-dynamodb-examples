package model

// MerchantStateProjection is shared by table provisioning and test fixtures.
// DynamoDB automatically projects the base and index keys.
func MerchantStateProjection() []string {
	return []string{"paymentId", "lastSequence", "correlationId", "amount", "currency", "updatedAtUtc", "reasonCode"}
}
