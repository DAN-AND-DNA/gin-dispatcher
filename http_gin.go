package server

import (
	"context"
	"fmt"
	ginprom "github.com/dan-and-dna/gin-prom"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	jsoniter "github.com/json-iterator/go"
	"github.com/prometheus/client_golang/prometheus"
	"go-dmm/pkg/minilog"
	"google.golang.org/grpc/status"
	"log"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var _ HttpServer = (*HttpServerImpl)(nil)

type HttpServerImpl struct {
	// 回调配置表
	callbacks atomic.Value // 回调

	// 减少反射生成协议类型的开销
	responseZeroValueCache map[uint64]*sync.Pool // 请求零值cache
	requestCache           map[uint64]*sync.Pool // 请求cache
	responseCache          map[uint64]*sync.Pool // 响应cache

	validate *validator.Validate
	logger   *minilog.MiniLog

	// gin的路由
	route *gin.Engine

	registry *prometheus.Registry

	ginMetrics *ginprom.Metrics

	listener net.Listener

	srv *http.Server
}

func NewHttpServer(listener net.Listener, logger *minilog.MiniLog) *HttpServerImpl {
	httpServer := &HttpServerImpl{}

	httpServer.callbacks.Store(map[uint64]reflect.Value{})
	httpServer.responseZeroValueCache = make(map[uint64]*sync.Pool)
	httpServer.requestCache = make(map[uint64]*sync.Pool)
	httpServer.responseCache = make(map[uint64]*sync.Pool)
	httpServer.validate = validator.New()

	// TODO 读配置
	httpServer.listener = listener
	httpServer.route = gin.Default()
	httpServer.srv = &http.Server{
		Handler: httpServer.route,
	}

	// 日志
	httpServer.logger = logger

	httpServer.registry = prometheus.NewRegistry()
	httpServer.ginMetrics = ginprom.NewMetrics("dmm", httpServer.registry)

	// 在这里添加中间件
	httpServer.route.Use(ginzap.RecoveryWithZap(httpServer.logger.GetZap(), true))
	httpServer.route.Use(ginzap.Ginzap(httpServer.logger.GetZap(), time.RFC3339, false))
	httpServer.route.Use(ginprom.Export(httpServer.ginMetrics))

	// TODO 写成中间件
	// 按id进行派发消息注册
	httpServer.route.POST("/zgame", httpServer.DispatchMsg)
	httpServer.route.POST("/ping", func(c *gin.Context) {
		panic("hhhh")
	})
	return httpServer
}

// Register 注册协议和对应的处理器
func (httpServer *HttpServerImpl) Register(protocolId uint64, handler any) {
	// 检查回调的参数数量和返回数量
	fn := reflect.ValueOf(handler)
	fnType := fn.Type()
	if fnType.Kind() != reflect.Func {
		panic("should be a function like func(context.Context, *Struct{}, *Struct{}) error")
	}

	if fnType.NumIn() != 3 {
		panic("should be a function like func(context.Context, *Struct{}, *Struct{}) error")
	}

	if fnType.NumOut() != 1 {
		panic("should be a function like func(context.Context, *Struct{}, *Struct{}) error")
	}

	// 检查函数参数类型
	ctxType := fnType.In(0)
	requestType := fnType.In(1).Elem()
	responseType := fnType.In(2).Elem()
	errType := fn.Type().Out(0)

	if !ctxType.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
		panic("first argument should be a context.Context")
	}

	if fnType.In(1).Kind() != reflect.Pointer {
		panic("second argument should be a pointer")
	}

	if fnType.In(2).Kind() != reflect.Pointer {
		panic("third argument should be a pointer")
	}

	if !errType.Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		panic("return argument should be a error")
	}

	// 注册请求
	httpServer.requestCache[protocolId] = &sync.Pool{
		New: func() any {
			return reflect.New(requestType)
		},
	}

	// 注册答复
	httpServer.responseCache[protocolId] = &sync.Pool{
		New: func() any {
			return reflect.New(responseType)
		},
	}

	httpServer.responseZeroValueCache[protocolId] = &sync.Pool{
		New: func() any {
			return reflect.Zero(responseType)
		},
	}

	// 注册回调
	callbacks := httpServer.callbacks.Load().(map[uint64]reflect.Value)
	newCallbacks := make(map[uint64]reflect.Value)

	for key, val := range callbacks {
		newCallbacks[key] = val
	}
	newCallbacks[protocolId] = fn

	httpServer.callbacks.Store(newCallbacks)
}

func (httpServer *HttpServerImpl) Run() {
	//err := httpServer.route.Run()
	err := httpServer.srv.Serve(httpServer.listener)
	if err != nil {
		panic(err)
	}
}

func (httpServer *HttpServerImpl) Close() {
	// TODO shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	httpServer.srv.Shutdown(ctx)
}

func (httpServer *HttpServerImpl) DispatchMsg(c *gin.Context) {
	// TODO 中间件做

	// FIXME gin底层拷贝出来这部分数据，安全但是性能不好
	m := c.Query("m")
	a := c.Query("a")
	if m != "snake" || a != "snake_require" {
		c.Abort()
		return
	}

	strMsgId := c.PostForm("msg_id")
	if len(strMsgId) > 10 {
		c.Abort()
		return
	}

	msgId, err := strconv.ParseUint(strMsgId, 10, 64)
	if err != nil {
		c.Abort()
		return
	}

	strMsg := c.PostForm("msg")
	if len(strMsg) == 0 {
		c.Abort()
		return
	}

	requestCachePool, ok := httpServer.requestCache[msgId]
	if !ok {
		log.Printf("unknown msg: %d\n", msgId)
		c.Abort()
		return
	}

	responseCachePool, ok := httpServer.responseCache[msgId]
	if !ok {
		log.Printf("unknown msg: %d\n", msgId)
		c.Abort()
		return
	}

	responsePool, ok := httpServer.responseZeroValueCache[msgId]
	if !ok {
		log.Printf("unknown msg: %d\n", msgId)
		c.Abort()
		return
	}

	// 拿请求
	request := requestCachePool.Get().(reflect.Value)
	defer requestCachePool.Put(request)

	// 拿响应
	response := responseCachePool.Get().(reflect.Value)
	defer func() {
		response.Elem().Set(responsePool.Get().(reflect.Value))
		responseCachePool.Put(response)
	}()

	// string强转[]byte，不走分配 (只读)
	var readOnlyBytesMsg []byte
	if runtime.Version() != "go1.20" {
		//	1.20  以下版本
		str := (*reflect.StringHeader)(unsafe.Pointer(&strMsg))
		ret := reflect.SliceHeader{Data: str.Data, Len: str.Len, Cap: str.Len}
		readOnlyBytesMsg = *(*[]byte)(unsafe.Pointer(&ret))
	} else {
		// 1.20 支持
		//readOnlyBytesMsg := unsafe.Slice(unsafe.StringData(strMsg), len(strMsg))
	}

	// 解析json
	newMsg := request.Interface()
	err = jsoniter.Unmarshal(readOnlyBytesMsg, newMsg)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusOK, gin.H{"errorCode": -1, "errorMessage": "bad json msg"})
		return
	}

	// TODO 写成中间件
	// 检查参数合法性
	err = httpServer.validate.Struct(newMsg)
	if err != nil {
		if _, ok := err.(*validator.InvalidValidationError); ok {
			fmt.Println(err)
			c.JSON(http.StatusOK, gin.H{"errorCode": -12, "errorMessage": "invalid request"})
			return
		}

		for _, err := range err.(validator.ValidationErrors) {
			fmt.Println(err.StructNamespace(), err.Field())
		}

		c.JSON(http.StatusOK, gin.H{"errorCode": -13, "errorMessage": "invalid json args"})
		return
	}

	// 拿对应的回调
	callbacks := httpServer.callbacks.Load().(map[uint64]reflect.Value)
	callback, ok := callbacks[msgId]
	if !ok {
		log.Printf("unknown msg callback: %d\n", msgId)
		c.Abort()
		return
	}

	// 调用函数
	inArgs := []reflect.Value{reflect.ValueOf(c), request, response}
	outArgs := callback.Call(inArgs)
	retVal := outArgs[0].Interface()
	err = nil
	if retVal != nil {
		err = retVal.(error)
	}
	// TODO 加强错误输出
	//httpServer.logger.Error("stack", zap.Error(err), zap.StackSkip("sss", 2))
	if err != nil {
		// 带错误码错误
		if s, ok := status.FromError(err); ok {
			c.JSON(200, gin.H{"errorCode": s.Code(), "errorMessage": s.Message()})
			return
		}

		// 普通错误
		c.JSON(200, gin.H{"errorCode": -1, "errorMessage": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response.Interface())
	return
}
