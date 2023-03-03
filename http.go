package server

type HttpServer interface {
	Register(protocolId uint64, handler any) // 注册协议
	Run()                                    // 运行
}
