<!--
PR 模板：每次开 Pull Request 自动带出来。
一个人开发没有同事 review，所以必须自己 review 自己 —— 这份清单就是你的「同事」。
-->

## 这个 PR 做了什么

<!-- 一两句话说清 -->

## 对应哪个 Sprint / Issue

- Sprint: S___
- Closes #

## 怎么验证

<!-- 给出可复制的命令或步骤，不要写「手动测过了」 -->

```bash
# 示例：
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"test001","password":"Test1234","nickname":"测试"}'
```

## 自查清单（合并前逐条打勾）

- [ ] 逐行看过 **Files changed**，每个改动都是我故意的
- [ ] 没有编辑器自动改的、误删的、调试用的 `fmt.Println` 残留
- [ ] **没有提交密码 / 密钥 / .env**（搜过 `password`、`secret`、`token`）
- [ ] CI 全绿
- [ ] 新功能有测试（核心逻辑必须有）
- [ ] 接口契约变了就同步更新了 `docs/api.md`
- [ ] 没有留下未处理的 `// TODO`（有就转成 Issue）
- [ ] **我能不看代码讲清这个改动**

## 截图（如果是前端改动）

<!-- 拖图片进来 -->
