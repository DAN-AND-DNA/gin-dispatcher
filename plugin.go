package server

import (
	"context"
)

// 魔改 https://github.com/go-kit/kit/endpoint

type Handler func(ctx context.Context, request any, response any) error

type Plugin func(next Handler) Handler

func Chain(outer Plugin, others ...Plugin) Plugin {
	return func(next Handler) Handler {
		for i := len(others) - 1; i >= 0; i-- { // reverse
			next = others[i](next)
		}
		return outer(next)
	}
}

func NopPlugin() Plugin {
	return func(next Handler) Handler {
		return func(ctx context.Context, request any, response any) error {
			return next(ctx, request, response)
		}
	}
}
