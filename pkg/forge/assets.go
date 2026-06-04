package forge

import (
	"fmt"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// localAsset 是待上傳的單一檔案: 本地路徑及發布時使用的 asset 名稱.
type localAsset struct {
	path string
	name string
}

// AssetUpload 實作: forgectl asset upload <repo> <version> <path>[=NAME]...
func (c *Client) AssetUpload(repo, version string, paths []string) error {
	files := make([]localAsset, len(paths))
	for i, spec := range paths {
		files[i] = parsePathSpec(spec)
	}

	p, err := c.platform(repo)
	if err != nil {
		return err
	}
	up, err := p.newUploader(version)
	if err != nil {
		return err
	}

	// 逐一嘗試每個檔案, 不提前中止; 統計結果, 若有失敗則於最後回傳錯誤 (docs/cli.md).
	var failed int
	for _, f := range files {
		if err := up.upload(f); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "上傳失敗 %s: %v\n", f.name, err)
			continue
		}
		fmt.Printf("已上傳 %s\n", f.name)
	}
	n := len(files)
	if failed > 0 {
		return fmt.Errorf("已上傳 %d/%d 個 asset; %d 個失敗", n-failed, n, failed)
	}
	fmt.Printf("已上傳 %d/%d 個 asset\n", n, n)
	return nil
}

// AssetDownload 實作:
// forgectl asset download <repo> <version> [pattern]... [-d DIR] [-o NAME] [--overwrite]
func (c *Client) AssetDownload(repo, version string, patterns []string, dir, output string, overwrite bool) error {
	p, err := c.platform(repo)
	if err != nil {
		return err
	}
	assets, err := p.findReleaseAssets(version)
	if err != nil {
		return err
	}

	matched := matchAssets(assets, patterns)
	if len(matched) == 0 {
		// 無 asset 符合條件視為成功: 沒有需要下載的內容, 以 exit 0 結束 (docs/cli.md).
		fmt.Println("沒有 asset 符合條件, 略過下載")
		return nil
	}
	if output != "" && len(matched) > 1 {
		return fmt.Errorf("-o/--output 指定單一檔名, 但有 %d 個 asset 符合條件; 請移除 -o 或縮小 pattern 範圍", len(matched))
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("建立目錄 %s: %w", dir, err)
		}
	}

	// 預先解析所有目的地並拒絕已存在的目標, 確保在任何檔案寫入前就發現衝突.
	type job struct {
		a    asset
		dest string
	}
	jobs := make([]job, 0, len(matched))
	for _, a := range matched {
		name := a.Name
		if output != "" {
			name = output
		}
		dest := name
		if dir != "" {
			dest = filepath.Join(dir, name)
		}
		if !overwrite {
			if _, err := os.Stat(dest); err == nil {
				return fmt.Errorf("%s 已存在; 請加上 --overwrite 以覆蓋", dest)
			}
		}
		jobs = append(jobs, job{a: a, dest: dest})
	}

	for _, j := range jobs {
		if err := downloadToFile(p, j.a, j.dest); err != nil {
			return err
		}
		fmt.Printf("已下載 %s -> %s\n", j.a.Name, j.dest)
	}
	return nil
}

// downloadToFile 將一個 asset 串流寫入暫存檔, 成功後才重新命名至 dest,
// 確保傳輸失敗時不會在 dest 留下不完整的檔案.
func downloadToFile(p platform, a asset, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".forgectl-*")
	if err != nil {
		return fmt.Errorf("建立暫存檔: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 重新命名成功後此呼叫為 no-op

	// os.CreateTemp 建立的檔案權限為 0600; 下載的 asset 應使用一般的
	// 0644, 使其與其他下載檔案一樣可被讀取.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := p.download(a, tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("下載 %s: %w", a.Name, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("儲存 %s: %w", dest, err)
	}
	return nil
}

// parsePathSpec 解析 "<path>[=NAME]" 上傳參數. 以最後一個 '=' 分割,
// 但若 '=' 後的文字看起來像路徑 (含路徑分隔符) 則整個參數視為純路徑,
// 讓路徑本身含 '=' 的情況在常見情境下仍可正常運作 (docs/cli.md). NAME 必須是平坦檔名.
// 若無可用的 "=NAME", asset 名稱取路徑的 basename.
func parsePathSpec(spec string) localAsset {
	if i := strings.LastIndex(spec, "="); i >= 0 {
		name := spec[i+1:]
		if name != "" && !strings.ContainsAny(name, `/\`) {
			return localAsset{path: spec[:i], name: name}
		}
	}
	return localAsset{path: spec, name: filepath.Base(spec)}
}

// matchAssets 回傳名稱符合任一 glob pattern 的 asset 清單.
// 無 pattern 時全部 asset 皆符合; 多個 pattern 取聯集; 不含萬用字元的 pattern 為精確比對
// (docs/cli.md).
func matchAssets(assets []asset, patterns []string) []asset {
	if len(patterns) == 0 {
		return assets
	}
	var out []asset
	for _, a := range assets {
		if matchAny(a.Name, patterns) {
			out = append(out, a)
		}
	}
	return out
}

// matchAny 回報 name 是否符合任一 glob pattern.
func matchAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// readLocalFile 讀取上傳來源檔案, 若路徑為目錄則以明確訊息拒絕
// (僅接受檔案; docs/cli.md). 上傳只讀取本地檔案, 不做任何修改.
func readLocalFile(p string) ([]byte, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s 是目錄, 不是檔案", p)
	}
	return os.ReadFile(p)
}

// contentType 由 asset 名稱猜測其 MIME type, 副檔名未知時預設回傳
// application/octet-stream (docs/cli.md).
func contentType(name string) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
