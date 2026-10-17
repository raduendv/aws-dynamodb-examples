package model

type Item struct {
	// Item contains the primary key attributes shared by DynamoDB entities.
	PK string `dynamodbav:"pk"` // Partition Key (e.g. "ACCOUNT#<id>", "PAYMENT#<id>")
	SK string `dynamodbav:"sk"` // Sort Key (e.g. "STATE", "EVENT#<timestamp>")
	//Type string // Discriminator: "Account", "Payment", "PaymentEvent", "IdempotencyRecord"
}
