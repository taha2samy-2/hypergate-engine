package engine

import (
	"context"
	"strings"
)

type Header struct {
	Key    string
	Value  string
	Append bool
}

type RequestContext struct {
	Ctx                  context.Context
	Path                 string
	Method               string
	Headers              map[string]string
	HeadersToAdd         []Header
	ResponseHeadersToAdd []Header
	HeadersToRemove      []string
	Blocked              bool
	ResponseStatus       int32
	ResponseBody         string
	UpstreamShadow       map[string]string
	DownstreamShadow     map[string]string
}

func (ctx *RequestContext) Reset() {
	ctx.Ctx = nil
	ctx.Path = ""
	ctx.Method = ""
	clear(ctx.Headers)
	ctx.HeadersToAdd = ctx.HeadersToAdd[:0]
	ctx.ResponseHeadersToAdd = ctx.ResponseHeadersToAdd[:0]
	ctx.HeadersToRemove = ctx.HeadersToRemove[:0]
	clear(ctx.UpstreamShadow)
	clear(ctx.DownstreamShadow)
	ctx.Blocked = false
	ctx.ResponseStatus = 0
	ctx.ResponseBody = ""
}

func (ctx *RequestContext) GetHeader(key string) string {
	key = strings.ToLower(key)
	if val, ok := ctx.UpstreamShadow[key]; ok {
		return val
	}
	for i := 0; i < len(ctx.HeadersToRemove); i++ {
		if ctx.HeadersToRemove[i] == key {
			return ""
		}
	}
	return ctx.Headers[key]
}

func (ctx *RequestContext) GetDownstreamHeader(key string) string {
	key = strings.ToLower(key)
	return ctx.DownstreamShadow[key]
}

func (ctx *RequestContext) SetHeaderUpstream(key, value string) {
	key = strings.ToLower(key)
	ctx.UpstreamShadow[key] = value

	for i := 0; i < len(ctx.HeadersToRemove); i++ {
		if ctx.HeadersToRemove[i] == key {
			ctx.HeadersToRemove[i] = ctx.HeadersToRemove[len(ctx.HeadersToRemove)-1]
			ctx.HeadersToRemove = ctx.HeadersToRemove[:len(ctx.HeadersToRemove)-1]
			break
		}
	}

	for i := 0; i < len(ctx.HeadersToAdd); i++ {
		if ctx.HeadersToAdd[i].Key == key {
			ctx.HeadersToAdd[i].Value = value
			return
		}
	}
	ctx.HeadersToAdd = append(ctx.HeadersToAdd, Header{Key: key, Value: value, Append: false})
}

func (ctx *RequestContext) RemoveHeaderUpstream(key string) {
	key = strings.ToLower(key)
	delete(ctx.UpstreamShadow, key)

	for i := 0; i < len(ctx.HeadersToAdd); i++ {
		if ctx.HeadersToAdd[i].Key == key {
			ctx.HeadersToAdd[i] = ctx.HeadersToAdd[len(ctx.HeadersToAdd)-1]
			ctx.HeadersToAdd = ctx.HeadersToAdd[:len(ctx.HeadersToAdd)-1]
			break
		}
	}

	for i := 0; i < len(ctx.HeadersToRemove); i++ {
		if ctx.HeadersToRemove[i] == key {
			return
		}
	}
	ctx.HeadersToRemove = append(ctx.HeadersToRemove, key)
}

func (ctx *RequestContext) SetHeaderDownstream(key, value string) {
	key = strings.ToLower(key)
	ctx.DownstreamShadow[key] = value

	for i := 0; i < len(ctx.ResponseHeadersToAdd); i++ {
		if ctx.ResponseHeadersToAdd[i].Key == key {
			ctx.ResponseHeadersToAdd[i].Value = value
			return
		}
	}
	ctx.ResponseHeadersToAdd = append(ctx.ResponseHeadersToAdd, Header{Key: key, Value: value, Append: false})
}

func (ctx *RequestContext) RemoveHeaderDownstream(key string) {
	key = strings.ToLower(key)
	delete(ctx.DownstreamShadow, key)

	for i := 0; i < len(ctx.ResponseHeadersToAdd); i++ {
		if ctx.ResponseHeadersToAdd[i].Key == key {
			ctx.ResponseHeadersToAdd[i] = ctx.ResponseHeadersToAdd[len(ctx.ResponseHeadersToAdd)-1]
			ctx.ResponseHeadersToAdd = ctx.ResponseHeadersToAdd[:len(ctx.ResponseHeadersToAdd)-1]
			break
		}
	}
}
