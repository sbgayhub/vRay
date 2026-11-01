package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
)

func updateCatalogs() {
	read := func(path string) map[string]string {
		raw, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		result := map[string]string{}
		if err := json.Unmarshal(raw, &result); err != nil {
			panic(err)
		}
		return result
	}
	source, english, traditional := read("locales/zh-Hans.json"), read("locales/en.json"), read("locales/zh-Hant.json")
	translations := read("tools/localize/messages.json")
	index := map[string]string{}
	for key, text := range source {
		index[text] = key
	}
	pairs := []string{}
	for _, pair := range strings.Fields("简簡 体體 订訂 阅閱 网網 络絡 连連 节節 点點 设設 规規 则則 语語 言言 换換 选選 择擇 时時 间間 传傳 输輸 显顯 隐隱 续續 数數 据據 关關 闭閉 开開 启啟 录錄 级級 别別 队隊 页頁 标標 题題 栏欄 折摺 叠疊 状狀 态態 查询 询詢 风風 险險 国國 区區 类類 型型 评評 测測 试試 载載 进進 度度 释釋 内內 存存 运運 过過 滤濾 导導 诊診 断斷 当當 属屬 来來 源源 余餘 额額 总總 量量 终終 结結 已已 验驗 证證 书書 钥鑰 默默 认認 际際 计計 划劃 处處 还還 复復 错錯 误誤 址址 务務 为為 与與 仅僅 从從 个個 条條 将將 并並 这這 里裡 单單 扩擴 实實 轻輕 材材 质質 纯純 强強 调調 玫玫 蓝藍 绿綠 红紅 琥琥 珀珀 靛靛 洋洋 图圖 颜顏 带帶 宽寬 损損 败敗 删刪 除除 编編 辑輯 段段 协協 议議 应應 该該 获獲 取取 满滿 范範 围圍 变變 东東 急急 临臨 占佔 云雲 步步 报報 体體 图圖 库庫 未未 准準 备備 向向 发發 灭滅 预預 划劃 移移 动動 暂暫 无無 总總 览覽 别別 号號 资資 产產 专專 链鏈 账賬 户戶 剩剩 亲親 爱愛 欢歡 迎迎 所所 获獲 权權 优優 势勢 虚虛 拟擬 卡卡 接接 管管 宽寬 帐帳 户戶 块塊 填填 写寫 随隨 难難 阻阻 减減 少少 迟遲 迟遲 待待 讯訊 息息 组組 键鍵 压壓 缩縮 跳跳 杂雜 散散 坏壞 后後 阈閾 编編 频頻 率率 缺缺 失失 查查 亚亞 汉漢 台臺 湾灣 黄黃 橙橙 警警 告告 尽盡 握握 惯慣 舒舒 适適 段段 浏瀏 览覽 路路 线線 坚堅 持持 结結 束束 续續 览覽 检檢 标標 击擊 左左 侧側 详詳 块塊 额額 触觸 应應 驱驅 动動 值值 码碼 许許 许許 阅閱 绩績 声聲 音音 选選 择擇 复複 覆覆 盖蓋 藏藏 历歷 史史 套套 属屬 链鏈 跃躍") {
		if pair == "查询" {
			continue
		}
		r := []rune(pair)
		if len(r) == 2 && r[0] != r[1] {
			pairs = append(pairs, string(r[0]), string(r[1]))
		}
	}
	convert := strings.NewReplacer(pairs...)
	for text, translation := range translations {
		key := index[text]
		if key == "" {
			hash := sha256.Sum256([]byte(text))
			key = "ui" + hex.EncodeToString(hash[:6])
			source[key] = text
		}
		english[key] = translation
		traditional[key] = convert.Replace(text)
	}
	// 旧目录可能用多个键保存相同的源文本。保持所有别名一致，避免映射遍历选中旧措辞。
	for key, text := range source {
		if translation, ok := translations[text]; ok {
			english[key] = translation
			traditional[key] = convert.Replace(text)
		}
	}
	source["homeMode"], english["homeMode"], traditional["homeMode"] = "模式", "Mode", "模式"
	for path, data := range map[string]map[string]string{"locales/zh-Hans.json": source, "locales/en.json": english, "locales/zh-Hant.json": traditional} {
		raw, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			panic(err)
		}
		raw = append(raw, '\n')
		if err := os.WriteFile(path, raw, 0644); err != nil {
			panic(err)
		}
	}
}
