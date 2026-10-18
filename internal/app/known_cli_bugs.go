package app

// Advisories stay on stderr and do not retry, suppress errors, or alter the raw
// connector response. Remove each when its linked issue is resolved and verified.
const (
	knownBugPCBConfig = "warning [bug #258]: Web 4.1.60 首次将系统规则转为自定义配置时，曾出现未请求孔间距微小变化；先保留 config get 快照，核对完整差分及保存重载后的配置，verified:false 仍为失败。https://github.com/zhoushoujianwork/easyeda-agent/issues/258"
)
