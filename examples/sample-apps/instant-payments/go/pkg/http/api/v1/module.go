package v1

import (
	"instantpayments/pkg/http"
	"instantpayments/pkg/http/api/v1/accounts"
	"instantpayments/pkg/http/api/v1/operations"
	"instantpayments/pkg/http/api/v1/payments"

	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("http.api.v1",
		fx.Provide(fx.Annotate(
			NewEchoHandler,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
		payments.Module(),
		accounts.Module(),
		operations.Module(),
	)
}

// GET	/api/v1/accounts/{accountId}/batch-get-reservations`
// GET	/api/v1/accounts/{accountId}/reservations`
// GET	/api/v1/accounts/{accountId}`
// GET	/api/v1/demo/dynamodb/access-patterns`
// GET	/api/v1/demo/dynamodb/conditional-writes`
// GET	/api/v1/demo/dynamodb/transactions`
// GET	/api/v1/operations/payments?state=PENDING_INVESTIGATION`
// POST	/api/v1/operations/reconcile/{paymentId}`
// GET	/api/v1/payments/{paymentId}/audit`
// GET	/api/v1/payments/inbound/{paymentId}`
// POST	/api/v1/payments/inbound/messages`
// GET	/api/v1/payments/outbound/{paymentId}`
// POST	/api/v1/payments/outbound`
// POST	/api/v1/simulator/config`
// POST	/api/v1/simulator/failures`
