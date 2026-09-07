package mail

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	maxHTMLBytes      = 4 << 20
	maxHTMLTokens     = 50000
	maxHTMLTokenBytes = 1 << 20
)

// Tokenize without constructing nodes. This bounds DOM
// allocation even when a small message contains hundreds of thousands of tags.
func validateHTMLBudget(input string) error {
	if len(input) > maxHTMLBytes {
		return fmt.Errorf("HTML input exceeds 4 MiB")
	}
	z := html.NewTokenizer(strings.NewReader(input))
	z.SetMaxBuf(maxHTMLTokenBytes)
	count := 0
	for {
		token := z.Next()
		if token == html.ErrorToken {
			if z.Err() == io.EOF {
				return nil
			}
			return fmt.Errorf("invalid HTML or HTML token exceeds 1 MiB")
		}
		count++
		if token == html.StartTagToken || token == html.SelfClosingTagToken {
			// Tokenizer raw-text rules can differ from DOM foreign-content rules.
			// Count embedded markup conservatively rather than undercounting it.
			z.NextIsNotRawText()
			_, more := z.TagName()
			for more {
				_, _, more = z.TagAttr()
				count++
			}
		}
		if count > maxHTMLTokens {
			return fmt.Errorf("HTML exceeds 50000 tokens or attributes")
		}
	}
}

type htmlOutput struct{ text strings.Builder }

func (w *htmlOutput) Write(p []byte) (int, error) {
	if len(p) > maxHTMLBytes-w.text.Len() {
		return 0, fmt.Errorf("sanitized HTML exceeds 4 MiB")
	}
	return w.text.Write(p)
}

// Rebuild an HTML5 parse tree using only inert formatting and validated URLs.
// No styles, foreign namespaces, forms, embedded documents, or event attributes
// survive. Rendering the rebuilt tree also escapes text and attribute values.
func sanitizeHTML(input string) (string, string, error) {
	if err := validateHTMLBudget(input); err != nil {
		return "", "", err
	}
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return "", "", fmt.Errorf("could not parse HTML body")
	}
	clean := &html.Node{Type: html.DocumentNode}
	var copyNode func(*html.Node, *html.Node)
	copyNode = func(src, dst *html.Node) {
		if src.Type == html.TextNode {
			dst.AppendChild(&html.Node{Type: html.TextNode, Data: src.Data})
			return
		}
		if src.Namespace != "" {
			return
		}
		if src.Type == html.ElementNode {
			switch src.Data {
			case "html", "body", "font", "center": // Preserve content of harmless wrappers.
			case "address", "article", "aside", "details", "figcaption", "figure", "footer", "header", "main", "mark", "nav", "section", "summary", "time", "a", "abbr", "b", "blockquote", "br", "caption", "code", "dd", "del", "div", "dl", "dt", "em", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre", "s", "small", "span", "strong", "sub", "sup", "table", "tbody", "td", "th", "thead", "tfoot", "tr", "u", "ul":
				n := &html.Node{Type: html.ElementNode, Data: src.Data, DataAtom: src.DataAtom}
				seen := map[string]bool{}
				for _, a := range src.Attr {
					if a.Namespace != "" || seen[a.Key] {
						continue
					}
					seen[a.Key] = true
					allowed := a.Key == "title" || (src.Data == "img" && a.Key == "alt")
					if (src.Data == "a" && a.Key == "href") || (src.Data == "img" && a.Key == "src") {
						a.Val = strings.TrimSpace(a.Val)
						allowed = safeHTMLURL(a.Val, src.Data == "img")
					}
					if (src.Data == "td" || src.Data == "th") && (a.Key == "colspan" || a.Key == "rowspan") {
						n, err := strconv.Atoi(a.Val)
						allowed = err == nil && n > 0 && n <= 1000
					}
					if allowed {
						n.Attr = append(n.Attr, a)
					}
				}
				dst.AppendChild(n)
				dst = n
			default:
				return
			}
		} else if src.Type != html.DocumentNode {
			return
		}
		for child := src.FirstChild; child != nil; child = child.NextSibling {
			copyNode(child, dst)
		}
	}
	copyNode(doc, clean)
	var out htmlOutput
	if err := html.Render(&out, clean); err != nil {
		return "", "", err
	}
	rendered := strings.TrimSpace(out.text.String())
	if err := validateHTMLBudget(rendered); err != nil {
		return "", "", err
	}
	return rendered, htmlToText(clean), nil
}

func safeHTMLURL(value string, image bool) bool {
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Hostname() != "" && u.User == nil
	case "mailto":
		return !image
	case "cid":
		return image
	default:
		return !image && strings.HasPrefix(value, "#")
	}
}

// Extract text directly from the bounded, sanitized tree without parsing again.
func htmlToText(doc *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			out.WriteString(n.Data)
		}
		block := false
		if n.Type == html.ElementNode {
			switch n.Data {
			case "blockquote", "br", "dd", "div", "dl", "dt", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "li", "ol", "p", "pre", "table", "tr", "ul":
				block = true
				out.WriteByte('\n')
			case "td", "th":
				out.WriteByte(' ')
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if block && n.Data != "br" {
			out.WriteByte('\n')
		}
	}
	walk(doc)
	return normalizeText(out.String())
}
