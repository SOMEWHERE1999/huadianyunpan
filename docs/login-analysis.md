# login-analysis.md

# 华电云盘登录流程分析（CAS + OAuth2）

## 1. 总体流程

``` text
浏览器
  ↓
https://pan.ncepu.edu.cn
  ↓302
https://ids.ncepu.edu.cn/authserver/login
  ↓
输入账号 + 密码 + 手机验证码
  ↓
CAS 登录成功（ST Ticket）
  ↓
/oauth2/signin?ticket=ST-xxxx
  ↓
OAuth2 Authorization Code
  ↓
/oauth2/login/callback
  ↓
AnyShare 首页
```

## 2. 组成模块

-   AnyShare Web
-   NCEPU CAS 统一身份认证
-   OAuth2 授权
-   Session/Cookie

## 3. 已确认接口

-   GET /authserver/login
-   POST /authserver/username-password/login
-   GET /oauth2/signin
-   GET /oauth2/auth
-   GET /oauth2/consent
-   GET /anyshare/oauth2/login/callback

## 4. 课程设计实现建议

第一版不实现自动登录。

程序仅提供：

``` go
func (p *AnyShareProvider) Login(ctx context.Context) error {
    return ErrInteractiveLoginRequired
}
```

等待未来接入官方开放认证接口。

## 5. 安全原则

-   不保存明文账号密码
-   不保存短信验证码
-   不记录 Cookie/Token
-   不关闭 TLS 校验
-   不绕过 CAS/OAuth2
