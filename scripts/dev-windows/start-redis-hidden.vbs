' ============================================================================
' start-redis-hidden.vbs —— 静默启动 redis-server（不弹控制台窗口）
'
' 为什么需要这个文件：
'   计划任务原来直接运行 redis-server.exe。因为任务的登录类型是
'   "仅在用户登录时运行"（Interactive），Windows 每次都会打开一个可见的
'   控制台窗口。两个问题：
'     1) 每次登录都弹一个黑窗口，很难看
'     2) 用户手滑关掉它，Redis 就死了，服务随之挂掉
'   通过 wscript + 窗口样式 0 启动即可隐藏。
'
' 为什么要设 CurrentDirectory（这一行不能删）：
'   redis.conf 必须以**相对路径**传入、并配合工作目录。
'   如果传绝对的 Windows 路径（C:\...），msys2 构建的 redis-server 会把它
'   当成 Unix 路径，报：
'     "# Fatal error, can't open config file '/qm/redis/C:\Users\qm\redis\redis.conf'"
'   这个坑在本地真实踩过。
'
' 刻意只用 ASCII：wscript 按 ANSI 读取 .vbs，中文会变乱码。
' ============================================================================

Dim sh
Set sh = CreateObject("WScript.Shell")
sh.CurrentDirectory = "C:\Users\qm\redis"
' 0 = 隐藏窗口，False = 不等待其退出
sh.Run """C:\Users\qm\redis\redis-server.exe"" redis.conf", 0, False
