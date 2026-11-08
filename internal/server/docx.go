package server

// docx.go —— 零依赖的 Word (.docx) 生成器。
// docx 本质是 ZIP 包内的 OOXML；本文件只做生成（不做解析），版式样式全部内置：
// 黑体标题层级 + 宋体正文（首行缩进）+ 单线边框表格（表头灰底）+ 页脚页码，
// 保证导出的运维报告版式严谨统一；报表模板（reptmpl.go）只负责内容与章节结构。

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
)

// docxBlock 报告内容块（由报表模板解析产生）
type docxBlock struct {
	Type  string // title=封面大标题 / h1=一级标题 / h2=二级标题 / cov=封面信息行 / covtable=封面信息表 / para=正文段落 / page=分页 / table=表格
	Text  string
	Table *docxTable
	Pairs [][2]string // covtable 用：标签/值对
}

// docxTable 数据表格（Rows 为空时渲染"本报告期无数据"占位行）
type docxTable struct {
	Headers []string
	Rows    [][]string
	Note    string // 表尾小字备注（如"仅显示最近 200 条"）
}

const docxXMLHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

const docxWNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

func escXML(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// docxPara 一个段落（样式见 styles.xml；text 为空输出空段）
func docxPara(text, style string) string {
	s := `<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	if text == "" {
		return s + `</w:p>`
	}
	return s + `<w:r><w:t xml:space="preserve">` + escXML(text) + `</w:t></w:r></w:p>`
}

// docxTblXML 表格：固定布局、列宽均分、表头灰底加粗、单线边框；表后必须有段落分隔
func docxTblXML(t *docxTable) string {
	if t == nil || len(t.Headers) == 0 {
		return ""
	}
	n := len(t.Headers)
	const totalWidth = 9638 // A4 可用宽度（twips），列宽均分
	var b bytes.Buffer
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblLayout w:type="fixed"/>` +
		`<w:tblBorders>` +
		`<w:top w:val="single" w:sz="4" w:space="0" w:color="666666"/>` +
		`<w:left w:val="single" w:sz="4" w:space="0" w:color="666666"/>` +
		`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="666666"/>` +
		`<w:right w:val="single" w:sz="4" w:space="0" w:color="666666"/>` +
		`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="999999"/>` +
		`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="999999"/>` +
		`</w:tblBorders><w:tblCellMar>` +
		`<w:top w:w="40" w:type="dxa"/><w:bottom w:w="40" w:type="dxa"/>` +
		`<w:left w:w="85" w:type="dxa"/><w:right w:w="85" w:type="dxa"/>` +
		`</w:tblCellMar></w:tblPr><w:tblGrid>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<w:gridCol w:w="%d"/>`, totalWidth/n)
	}
	b.WriteString(`</w:tblGrid>`)

	// 表头行（跨页重复）
	b.WriteString(`<w:tr><w:trPr><w:tblHeader/></w:trPr>`)
	for _, h := range t.Headers {
		b.WriteString(`<w:tc><w:tcPr><w:shd w:val="clear" w:color="auto" w:fill="EDEDF2"/>` +
			`<w:vAlign w:val="center"/></w:tcPr>` +
			`<w:p><w:pPr><w:pStyle w:val="TC"/></w:pPr><w:r><w:rPr><w:b/></w:rPr>` +
			`<w:t xml:space="preserve">` + escXML(h) + `</w:t></w:r></w:p></w:tc>`)
	}
	b.WriteString(`</w:tr>`)

	rows := t.Rows
	if len(rows) == 0 { // 空表占位：首格提示，其余留空
		row := make([]string, n)
		row[0] = "（本报告期无数据或该项未启用）"
		rows = [][]string{row}
	}
	for _, row := range rows {
		b.WriteString(`<w:tr>`)
		for i := 0; i < n; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString(`<w:tc><w:tcPr><w:vAlign w:val="center"/></w:tcPr>` +
				`<w:p><w:pPr><w:pStyle w:val="TC"/></w:pPr>` +
				`<w:r><w:t xml:space="preserve">` + escXML(cell) + `</w:t></w:r></w:p></w:tc>`)
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	if t.Note != "" {
		b.WriteString(docxPara(t.Note, "NT"))
	} else {
		b.WriteString(`<w:p/>`) // 相邻表格必须有段落分隔，否则 Word 合并两表
	}
	return b.String()
}

// docxCovTable 封面信息表：无边框两列（标签列右对齐、值列左对齐），整表在页面居中，
// 行距舒适——比逐行居中段落更整齐（同列文字纵向对齐）
func docxCovTable(pairs [][2]string) string {
	if len(pairs) == 0 {
		return ""
	}
	const labelW, valueW, totalW = 2400, 4200, 6600 // A4 版心内约 11.6cm，居中
	var b bytes.Buffer
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="` + strconv.Itoa(totalW) + `" w:type="dxa"/><w:jc w:val="center"/>` +
		`<w:tblBorders>` +
		`<w:top w:val="none" w:sz="0" w:space="0" w:color="auto"/>` +
		`<w:left w:val="none" w:sz="0" w:space="0" w:color="auto"/>` +
		`<w:bottom w:val="none" w:sz="0" w:space="0" w:color="auto"/>` +
		`<w:right w:val="none" w:sz="0" w:space="0" w:color="auto"/>` +
		`<w:insideH w:val="none" w:sz="0" w:space="0" w:color="auto"/>` +
		`<w:insideV w:val="none" w:sz="0" w:space="0" w:color="auto"/>` +
		`</w:tblBorders><w:tblLayout w:type="fixed"/>` +
		`<w:tblCellMar><w:top w:w="60" w:type="dxa"/><w:bottom w:w="60" w:type="dxa"/>` +
		`<w:left w:w="85" w:type="dxa"/><w:right w:w="85" w:type="dxa"/></w:tblCellMar></w:tblPr>` +
		`<w:tblGrid><w:gridCol w:w="` + strconv.Itoa(labelW) + `"/><w:gridCol w:w="` + strconv.Itoa(valueW) + `"/></w:tblGrid>`)
	for _, p := range pairs {
		b.WriteString(`<w:tr><w:tc><w:tcPr><w:vAlign w:val="center"/></w:tcPr>` +
			`<w:p><w:pPr><w:pStyle w:val="TC"/><w:jc w:val="right"/></w:pPr>` +
			`<w:r><w:t xml:space="preserve">` + escXML(p[0]) + `：</w:t></w:r></w:p></w:tc>` +
			`<w:tc><w:tcPr><w:vAlign w:val="center"/></w:tcPr>` +
			`<w:p><w:pPr><w:pStyle w:val="TC"/><w:jc w:val="left"/></w:pPr>` +
			`<w:r><w:t xml:space="preserve">` + escXML(p[1]) + `</w:t></w:r></w:p></w:tc></w:tr>`)
	}
	b.WriteString(`</w:tbl><w:p/>`) // 表格后必须有段落分隔
	return b.String()
}

// docxStyles 内置样式表：Normal 正文（宋体 11pt 首行缩进两字符 1.5 倍行距）、
// Title 封面标题（黑体 22pt 居中）、H1/H2 标题（黑体 15/13pt）、
// COV 封面信息行（12pt 居中）、TC 表格单元（10.5pt 居中单倍行距）、NT 表注（9pt 灰色）
const docxStyles = docxXMLHeader + `<w:styles ` + docxWNS + `>` +
	`<w:docDefaults><w:rPrDefault><w:rPr>` +
	`<w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体" w:cs="Times New Roman"/>` +
	`<w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US" w:eastAsia="zh-CN"/>` +
	`</w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:line="360" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +

	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:spacing w:after="60"/><w:ind w:firstLineChars="200" w:firstLine="440"/><w:jc w:val="both"/></w:pPr></w:style>` +

	`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:spacing w:before="1200" w:after="720" w:line="240" w:lineRule="auto"/>` +
	`<w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="center"/></w:pPr>` +
	`<w:rPr><w:rFonts w:eastAsia="黑体"/><w:b/><w:sz w:val="44"/><w:szCs w:val="44"/></w:rPr></w:style>` +

	`<w:style w:type="paragraph" w:styleId="H1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="280" w:after="140" w:line="240" w:lineRule="auto"/>` +
	`<w:ind w:firstLineChars="0" w:firstLine="0"/><w:outlineLvl w:val="0"/></w:pPr>` +
	`<w:rPr><w:rFonts w:eastAsia="黑体"/><w:b/><w:sz w:val="30"/><w:szCs w:val="30"/></w:rPr></w:style>` +

	`<w:style w:type="paragraph" w:styleId="H2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="200" w:after="100" w:line="240" w:lineRule="auto"/>` +
	`<w:ind w:firstLineChars="0" w:firstLine="0"/><w:outlineLvl w:val="1"/></w:pPr>` +
	`<w:rPr><w:rFonts w:eastAsia="黑体"/><w:b/><w:sz w:val="26"/><w:szCs w:val="26"/></w:rPr></w:style>` +

	`<w:style w:type="paragraph" w:styleId="COV"><w:name w:val="Cover Line"/><w:basedOn w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:spacing w:after="120"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="center"/></w:pPr>` +
	`<w:rPr><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>` +

	`<w:style w:type="paragraph" w:styleId="TC"><w:name w:val="Table Cell"/><w:qFormat/>` +
	`<w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/>` +
	`<w:jc w:val="center"/></w:pPr>` +
	`<w:rPr><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr></w:style>` +

	`<w:style w:type="paragraph" w:styleId="NT"><w:name w:val="Table Note"/><w:basedOn w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:spacing w:after="120"/><w:ind w:firstLineChars="0" w:firstLine="0"/></w:pPr>` +
	`<w:rPr><w:color w:val="808080"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr></w:style>` +
	`</w:styles>`

// docxSectPr A4 版面 + 页脚引用（rId2 = footer1.xml）
const docxSectPr = `<w:sectPr><w:footerReference w:type="default" r:id="rId2"/>` +
	`<w:pgSz w:w="11906" w:h="16838"/>` +
	`<w:pgMar w:top="1440" w:right="1310" w:bottom="1440" w:left="1310" w:header="851" w:footer="992" w:gutter="0"/>` +
	`<w:cols w:space="425"/><w:docGrid w:linePitch="312"/></w:sectPr>`

const docxFooter = docxXMLHeader + `<w:ftr ` + docxWNS + `>` +
	`<w:p><w:pPr><w:jc w:val="center"/></w:pPr>` +
	`<w:r><w:rPr><w:sz w:val="18"/></w:rPr><w:fldChar w:fldCharType="begin"/></w:r>` +
	`<w:r><w:rPr><w:sz w:val="18"/></w:rPr><w:instrText xml:space="preserve"> PAGE </w:instrText></w:r>` +
	`<w:r><w:rPr><w:sz w:val="18"/></w:rPr><w:fldChar w:fldCharType="end"/></w:r>` +
	`</w:p></w:ftr>`

// docxTitle 封面标题行：连续多个 # 标题（如"单位名称"+"报告名"两行）时仅收窄行间间距，
// 首行保留与页首、末行保留与下文信息表的既有留白；单标题用 Title 样式默认间距。
func docxTitle(blocks []docxBlock, i int) string {
	prevT := i > 0 && blocks[i-1].Type == "title"
	nextT := i+1 < len(blocks) && blocks[i+1].Type == "title"
	if !prevT && !nextT {
		return docxPara(blocks[i].Text, "Title")
	}
	before, after := 240, 240 // 行间 12pt+12pt，紧凑但不贴死
	if !prevT {
		before = 1200
	}
	if !nextT {
		after = 720
	}
	return fmt.Sprintf(`<w:p><w:pPr><w:pStyle w:val="Title"/>`+
		`<w:spacing w:before="%d" w:after="%d" w:line="240" w:lineRule="auto"/></w:pPr>`+
		`<w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, before, after, escXML(blocks[i].Text))
}

// BuildDocx 把内容块渲染为完整 .docx（zip 包）
func BuildDocx(blocks []docxBlock) ([]byte, error) {
	var body bytes.Buffer
	for i, bl := range blocks {
		switch bl.Type {
		case "title":
			body.WriteString(docxTitle(blocks, i))
		case "h1":
			body.WriteString(docxPara(bl.Text, "H1"))
		case "h2":
			body.WriteString(docxPara(bl.Text, "H2"))
		case "cov":
			body.WriteString(docxPara(bl.Text, "COV"))
		case "covtable":
			body.WriteString(docxCovTable(bl.Pairs))
		case "page":
			body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
		case "table":
			body.WriteString(docxTblXML(bl.Table))
		default: // para
			body.WriteString(docxPara(bl.Text, "Normal"))
		}
	}
	doc := docxXMLHeader + `<w:document ` + docxWNS + `><w:body>` +
		body.String() + docxSectPr + `</w:body></w:document>`

	contentTypes := docxXMLHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`<Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>` +
		`</Types>`

	rels := docxXMLHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`</Relationships>`

	docRels := docxXMLHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>` +
		`</Relationships>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct {
		name string
		data string
	}{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rels},
		{"word/document.xml", doc},
		{"word/styles.xml", docxStyles},
		{"word/_rels/document.xml.rels", docRels},
		{"word/footer1.xml", docxFooter},
	}
	for _, f := range files {
		fw, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write([]byte(f.data)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
