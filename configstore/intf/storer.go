package intf

import (
	"context"
)

type ConfigObjectDecoder func(data []byte) (any, error)

type ConfigStorer interface {
	GetCredentialStore(ctx context.Context, decodeConfigObject ConfigObjectDecoder) (CredentialStorer, error)

	GetProviderConfigStore(ctx context.Context, decodeProviderConfig ConfigObjectDecoder) (ProviderConfigStorer, error)

	GetVirtualKeyStore(ctx context.Context, decodeVirtualKey ConfigObjectDecoder) (VirtualKeyStorer, error)

	GetRouteStore(ctx context.Context, decodeRoute ConfigObjectDecoder) (RouteStorer, error)
}
