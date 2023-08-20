package main

import (
	"context"
	"fmt"
	ginDispatcher "github.com/dan-and-dna/gin-dispatcher"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"log"
	"net/http"
)

type EchoRequest struct {
	Message string `json:"message" form:"message" binding:"required"`
}

type EchoResponse struct {
	Message string `json:"message"`
}

func main() {
	r := gin.Default()

	messages := ginDispatcher.NewMessages(
		plugin("hello"),
		plugin("world"),
	)

	messages.MessageId = func(c *gin.Context) string {
		module := c.Param("module")
		message := c.Param("message")
		return fmt.Sprintf("%s::%s", module, message)
	}
	messages.ShouldBind = func(c *gin.Context, req any) error {
		return c.ShouldBind(req)
	}
	messages.HandleError = func(c *gin.Context, err error) {
		if _, ok := err.(validator.ValidationErrors); ok {
			c.JSON(http.StatusOK, gin.H{"code": -1, "error": "invalid args"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": -1, "error": err.Error()})
	}

	messages.Register("test::echo", func(c context.Context, req *EchoRequest, resp *EchoResponse) error {
		log.Println("echo")
		resp.Message = req.Message
		return nil
	})

	r.POST("/:module/:message", ginDispatcher.GinDispatcher(messages))

	// curl http://127.0.0.1:8080/test/echo?message=你好 hello
	r.GET("/:module/:message", ginDispatcher.GinDispatcher(messages))

	log.Fatalln(r.Run())
}

func plugin(newMessage string) ginDispatcher.Plugin {
	return func(next ginDispatcher.Handler) ginDispatcher.Handler {
		return func(ctx context.Context, request any, response any) error {
			log.Println("start")
			defer log.Println("end")
			r := request.(*EchoRequest)
			log.Println(r.Message)
			r.Message = newMessage
			return next(ctx, request, response)
		}
	}
}
