package proxyhostsvc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/proxyhostsvc"
)

func TestProxyHostSvc_Validation(t *testing.T) {
	svc := proxyhostsvc.New(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	// Missing node_id
	_, err := svc.Create(ctx, proxyhostsvc.CreateProxyHostInput{
		DomainNames: []string{"test.example.com"},
	}, "user-1")
	require.NotNil(t, err)
	assert.Contains(t, err.Message, "网关节点必填")

	// Empty domain names
	_, err = svc.Create(ctx, proxyhostsvc.CreateProxyHostInput{
		NodeID: "node-1",
	}, "user-1")
	require.NotNil(t, err)
	assert.Contains(t, err.Message, "至少输入一个域名")

	// Invalid domain name
	_, err = svc.Create(ctx, proxyhostsvc.CreateProxyHostInput{
		NodeID:      "node-1",
		DomainNames: []string{"invalid..domain"},
	}, "user-1")
	require.NotNil(t, err)
	assert.Contains(t, err.Message, "格式非法")

	// Missing forward host
	_, err = svc.Create(ctx, proxyhostsvc.CreateProxyHostInput{
		NodeID:      "node-1",
		DomainNames: []string{"blog.example.com"},
	}, "user-1")
	require.NotNil(t, err)
	assert.Contains(t, err.Message, "转发目标主机必填")
}
