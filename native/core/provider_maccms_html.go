package core

import (
	"strings"

	"golang.org/x/net/html"
)

// 苹果CMS 采集适配器用的极简 DOM 工具（0.8.11）。
//
// 为什么这里要真解析 DOM，而不是像 E站那样用正则：
//
//  1. 苹果CMS 的模板会给 class 名加**每站随机的前缀**，例如花果的
//     `_9dbe81752a1dbf17-vodlist__item`、发果的 `stui-vodlist__box` 前面
//     还挂着一串随机类名，正则只能靠"后缀匹配"硬猜，容器边界一错位整页全废。
//  2. 发果这类站会在结构里插 `<var>`/`<samp>`/`<tt>`/`<acronym>` 之类噪声标签，
//     正则的 `.*?` 很容易被它们截断。
//
// 解析成树之后，规则可以只依赖"class 后缀"和"子树里有几个详情链接"，
// 这两个特征在四个实测站上都是稳定的。
func maccmsDocument(body string) *html.Node {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	node, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	return node
}

// maccmsNodes 按前序遍历收集满足条件的节点。
func maccmsNodes(root *html.Node, match func(*html.Node) bool) []*html.Node {
	if root == nil || match == nil {
		return nil
	}
	var nodes []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && match(node) {
			nodes = append(nodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return nodes
}

// maccmsFind 返回第一个满足条件的节点。
func maccmsFind(root *html.Node, match func(*html.Node) bool) *html.Node {
	if root == nil || match == nil {
		return nil
	}
	if root.Type == html.ElementNode && match(root) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := maccmsFind(child, match); found != nil {
			return found
		}
	}
	return nil
}

func maccmsAttr(node *html.Node, names ...string) string {
	if node == nil {
		return ""
	}
	for _, name := range names {
		for _, attribute := range node.Attr {
			if strings.EqualFold(attribute.Key, name) {
				if value := strings.TrimSpace(attribute.Val); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

// maccmsClasses 把 class 属性拆成集合，用来做"后缀匹配"。
func maccmsClasses(node *html.Node) []string {
	if node == nil {
		return nil
	}
	return strings.Fields(maccmsAttr(node, "class"))
}

// maccmsHasClass 判断节点是否带某个 class。
//
// 苹果CMS 模板普遍给类名加随机前缀（`xxx-vodlist__item`），所以除了
// 精确相等，也认"以 `-<name>` 结尾"和"以 `_<name>` 结尾"两种形态。
func maccmsHasClass(node *html.Node, name string) bool {
	if name == "" {
		return false
	}
	for _, value := range maccmsClasses(node) {
		if value == name || strings.HasSuffix(value, "-"+name) || strings.HasSuffix(value, "_"+name) {
			return true
		}
	}
	return false
}

// maccmsText 取节点的可见文本，折叠空白。
//
// script/style 里的内容不算可见文本：苹果CMS 的播放页会把整段播放器配置
// 塞在 `<script>` 里，混进来会把标题、简介污染成代码。
func maccmsText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
			return
		}
		if current.Type == html.ElementNode {
			switch strings.ToLower(current.Data) {
			case "script", "style", "noscript":
				return
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}

// maccmsOwnText 只取节点的直接文本子节点，不含后代元素。
func maccmsOwnText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			builder.WriteString(child.Data)
			builder.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func maccmsTag(node *html.Node, names ...string) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	for _, name := range names {
		if strings.EqualFold(node.Data, name) {
			return true
		}
	}
	return false
}

// maccmsMeta 取 `<meta name=…|property=… content=…>` 的 content。
func maccmsMeta(root *html.Node, keys ...string) string {
	node := maccmsFind(root, func(current *html.Node) bool {
		if !maccmsTag(current, "meta") {
			return false
		}
		key := maccmsAttr(current, "name", "property", "itemprop")
		for _, wanted := range keys {
			if strings.EqualFold(key, wanted) {
				return true
			}
		}
		return false
	})
	if node == nil {
		return ""
	}
	return maccmsAttr(node, "content")
}
