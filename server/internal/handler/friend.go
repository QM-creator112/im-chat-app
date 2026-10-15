package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// FriendHandler 好友接口。
type FriendHandler struct {
	svc *service.FriendService
}

// NewFriendHandler 构造 FriendHandler。
func NewFriendHandler(svc *service.FriendService) *FriendHandler {
	return &FriendHandler{svc: svc}
}

type addFriendReq struct {
	FriendID uint64 `json:"friendId" binding:"required"`
}

// List 处理 GET /api/v1/friends
func (h *FriendHandler) List(c *gin.Context) {
	uid := MustUserID(c)

	friends, err := h.svc.ListFriends(c.Request.Context(), uid)
	if err != nil {
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}

	OK(c, Page{Items: friends, Total: len(friends)})
}

// Add 处理 POST /api/v1/friends
//
// 语义是**幂等**的：重复添加同一好友不报错，直接当成功。
// 因为用户可能连点两次，或者双方同时互相添加。
func (h *FriendHandler) Add(c *gin.Context) {
	var req addFriendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadParam(c, "请求体格式错误，需要 friendId")
		return
	}

	uid := MustUserID(c)
	err := h.svc.AddFriend(c.Request.Context(), uid, req.FriendID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCannotAddSelf):
			FailBadParam(c, err.Error())
		case errors.Is(err, service.ErrUserNotFound):
			// 契约里没有「目标用户不存在」的专用码。
			// 这本质上是客户端传了无效的 friendId，归到参数错误最一致。
			FailBadParam(c, "目标用户不存在")
		default:
			c.Error(err) //nolint:errcheck
			FailInternal(c)
		}
		return
	}

	OK(c, gin.H{})
}
