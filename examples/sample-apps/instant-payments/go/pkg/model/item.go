package model

// Item carries the discriminator shared by entities in the single table.
type Item struct {
	EntityType string `dynamodbav:"entityType"`
}
