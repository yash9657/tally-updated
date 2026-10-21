package server

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/khata/cored/internal/agent"
)

// Describe only the HTTP types used by previewable handlers. Reflecting JSON
// tags keeps nested field names/types aligned with what those handlers decode.
// This is a shape guide, not a substitute for their conditional validation.
func agentActionSchema(id string) json.RawMessage {
	if !agent.Previewable(id) {
		return nil
	}
	var body any
	switch {
	case strings.HasPrefix(id, "post.vouchers."), strings.HasPrefix(id, "put.vouchers."):
		body = voucherReq{}
	case id == "post.ledgers":
		body = ledgerReq{}
	case id == "post.stock-items":
		body = stockItemReq{}
	case id == "post.groups":
		body = groupReq{}
	case id == "post.units":
		body = unitReq{}
	case id == "post.cost-categories":
		body = costCategoryReq{}
	case id == "post.budgets":
		body = budgetReq{}
	case id == "put.stock-items._stockitemid.valuation-method":
		body = valuationMethodReq{}
	case id == "post.sales-orders", id == "post.purchase-orders":
		body = orderReq{}
	case id == "put.sales-orders._orderid.close", id == "put.purchase-orders._orderid.close":
		body = closeOrderReq{}
	case id == "put.bank-reconciliation._ledgerid._voucherid._legno":
		body = struct {
			BankDate string `json:"bank_date"`
		}{}
	default:
		return nil
	}
	raw, _ := json.Marshal(agentWireSchema(reflect.TypeOf(body)))
	return raw
}

func agentWireSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.IsExported() && name != "" && name != "-" {
				props[name] = agentWireSchema(f.Type)
			}
		}
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": agentWireSchema(t.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	default:
		return map[string]any{"type": "string"}
	}
}
