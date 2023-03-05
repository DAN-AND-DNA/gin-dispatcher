package server

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	jsoniter "github.com/json-iterator/go"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

type Messages struct {
	handlers atomic.Value // 回调

	requestCache  map[string]*sync.Pool // 请求cache
	responseCache map[string]*sync.Pool // 响应cache
	validate      *validator.Validate

	MessageId   func(*gin.Context) string
	Payload     func(*gin.Context) string
	HandleError func(*gin.Context, error)
}

func NewMessages() *Messages {
	httpServer := &Messages{}

	httpServer.handlers.Store(map[string]reflect.Value{})
	httpServer.requestCache = make(map[string]*sync.Pool)
	httpServer.responseCache = make(map[string]*sync.Pool)
	httpServer.validate = validator.New()

	// default
	httpServer.MessageId = func(c *gin.Context) string {
		return c.PostForm("id")
	}
	httpServer.Payload = func(c *gin.Context) string {
		return c.PostForm("payload")
	}
	httpServer.HandleError = func(c *gin.Context, err error) {
		c.String(http.StatusInternalServerError, err.Error())
	}

	return httpServer
}

// Register 注册协议和对应的处理器
func (messages *Messages) Register(messageId string, handler any) {
	// 检查回调的参数数量和返回数量
	fn := reflect.ValueOf(handler)
	fnType := fn.Type()
	if fnType.Kind() != reflect.Func {
		panic("Handler should be 'func(context.Context, *Struct{}, *Struct{}) error'")
	}

	if fnType.NumIn() != 3 {
		panic("Handler should be 'func(context.Context, *Struct{}, *Struct{}) error'")
	}

	if fnType.NumOut() != 1 {
		panic("Handler should be 'func(context.Context, *Struct{}, *Struct{}) error'")
	}

	// 检查函数参数类型
	ctxType := fnType.In(0)
	requestType := fnType.In(1).Elem()
	responseType := fnType.In(2).Elem()
	errType := fn.Type().Out(0)

	if !ctxType.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
		panic("First argument should be a context.Context")
	}

	if fnType.In(1).Kind() != reflect.Pointer {
		panic("Second argument should be a pointer")
	}

	if fnType.In(2).Kind() != reflect.Pointer {
		panic("Third argument should be a pointer")
	}

	if !errType.Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		panic("Function should returns a error")
	}

	// 注册请求
	messages.requestCache[messageId] = &sync.Pool{
		New: func() any {
			return reflect.New(requestType)
		},
	}

	// 注册答复
	messages.responseCache[messageId] = &sync.Pool{
		New: func() any {
			return reflect.New(responseType)
		},
	}

	// 注册回调
	callbacks := messages.handlers.Load().(map[string]reflect.Value)
	newCallbacks := make(map[string]reflect.Value)

	for key, val := range callbacks {
		newCallbacks[key] = val
	}
	newCallbacks[messageId] = fn

	messages.handlers.Store(newCallbacks)
}

func GinDispatcher(messages *Messages) gin.HandlerFunc {
	return func(c *gin.Context) {
		if messages.MessageId == nil || messages.Payload == nil {
			c.Abort()
			return
		}

		messageId := messages.MessageId(c)
		payload := messages.Payload(c)

		requestCachePool, ok := messages.requestCache[messageId]
		if !ok {
			c.Abort()
			return
		}

		responseCachePool, ok := messages.responseCache[messageId]
		if !ok {
			c.Abort()
			return
		}

		// 拿请求
		request := requestCachePool.Get().(reflect.Value)
		defer func() {
			request.Elem().SetZero()
			requestCachePool.Put(request)
		}()

		// 拿响应
		response := responseCachePool.Get().(reflect.Value)
		defer func() {
			response.Elem().SetZero()
			responseCachePool.Put(response)
		}()

		newMsg := request.Interface()
		// string强转[]byte，不走分配 (只读)
		//	1.20  以下版本
		if payload != "" {
			str := (*reflect.StringHeader)(unsafe.Pointer(&payload))
			ret := reflect.SliceHeader{Data: str.Data, Len: str.Len, Cap: str.Len}
			bytesPayload := *(*[]byte)(unsafe.Pointer(&ret))
			// 1.20 支持
			//bytesPayload := unsafe.Slice(unsafe.StringData(strMsg), len(strMsg))

			// 解析json
			err := jsoniter.Unmarshal(bytesPayload, newMsg)
			if err != nil {
				messages.HandleError(c, err)
				return
			}
		}

		// 检查参数合法性
		err := messages.validate.Struct(newMsg)
		if err != nil {
			messages.HandleError(c, err)
			return
		}

		// 拿对应的回调
		handlers := messages.handlers.Load().(map[string]reflect.Value)
		handler, ok := handlers[messageId]
		if !ok {
			c.Abort()
			return
		}

		// 调用函数
		ins := []reflect.Value{reflect.ValueOf(c), request, response}
		outs := handler.Call(ins)
		out := outs[0].Interface()
		if out != nil {
			err = out.(error)
		}

		if err != nil {
			messages.HandleError(c, err)
			return
		}

		c.JSON(http.StatusOK, response.Interface())
		return
	}
}
