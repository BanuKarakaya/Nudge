package handler

import (
	"context"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

func (h *WorkspaceHandler) Guide(ctx context.Context, user int64, team string) (service.WorkspaceGuide, error) {
	m, err := h.store.Member(ctx, user)
	if err != nil || m.UserID != user || m.Team != team {
		return service.WorkspaceGuide{}, workspace.ErrDenied
	}
	g := service.WorkspaceGuide{Manager: m.Manager}
	add := func(topic, section string, available, manager bool, instructions string) {
		g.Features = append(g.Features, service.WorkspaceFeature{Topic: topic, Section: section, Available: available, ManagerOnly: manager, Instructions: instructions})
	}
	add("dashboard", "Genel bakış", true, false, "workspace_connect ile kişisel giriş bağlantısını DM'den gönder. Link tek kullanımlık ve 20 dakika geçerlidir; süresi dolduysa yenisini isteyebilir. Panel girişi sağlayıcı hesabını bağlamaz.")
	for _, kind := range []string{"github", "gitlab", "jira", "trello", "feedbin", "raindrop"} {
		p, ok := h.provider(kind)
		instruction := "Yönetici Entegrasyonlar bölümünde servisi ve ortak alanları etkinleştirir. Üye Hesaplarım bölümünde kendi hesabını bağlar; Yeniden doğrula ile yeniler, Hesap bağlantımı kaldır ile yalnızca kişisel bağlantısını kaldırır."
		if kind == "feedbin" || kind == "raindrop" {
			instruction = "Kişisel bağlantıdır; ortak repo/proje/pano seçimi veya çalışma alanı yöneticisinin servisi etkinleştirmesi gerekmez. available_in_this_installation=true ise sunucu önkoşulları hazırdır; yönetici etkinleştirmesini bekleme. Hesaplarım → " + kind + " → Hesabını doğrula/Yeniden doğrula. Feedbin bilgileri sadece güvenli web formuna girilir; Raindrop kendi OAuth ekranında yetkilendirilir. Hesap bağlantımı kaldır yalnızca Nudgy'de saklanan kişisel erişimi kaldırır; sağlayıcıdaki içerikleri silmez."
		}
		if ok {
			instruction = p.Name + ": " + instruction
		}
		add(kind, "Hesaplarım", ok, false, instruction+" Bu rehber bağlantının geçerliliğini test etmez; bağlı olduğunu iddia etme.")
	}
	add("usage", "Kullanım", h.analytics != nil || h.monitor != nil, false, "Gerçek model, token, kullanım sıklığı burada görüntülenir. Üye kendi kullanımını, yönetici ekip genelini ve kullanıcı dağılımını görebilir. Token tüketimi fatura tutarı değildir; bu rehber canlı metrik döndürmez.")
	add("logs", "Canlı işlemler", h.monitor != nil, false, "İsteğin ayrıntısında aşamalar, kullanılan araçlar, hata kodları ve Slack teslim durumu izlenir. Üye kendi isteklerini, yönetici ekip isteklerini görür. Ham konuşmalar/anahtarlar gösterilmez; bu rehber hata nedeni veya durum verisi döndürmez.")
	_, prefs := h.store.(preferencesStore)
	add("reminders", "Hatırlatıcılar", prefs, true, "Çalışma alanı yöneticisi günlük hatırlatıcıları açıp kapatır ve IANA saat dilimini kaydeder. Bağlantı özeti 22:00, RSS başlıkları 23:00; saatler serbestçe değiştirilemez. Bu ayar kullanıcıya özel veya tek seferlik görev değildir. Slack'ten ayar değiştirme aracı yok; panelde Kaydet gerekir.")
	add("ai", "AI ayarları", h.ai != nil, true, "Yönetici sağlayıcı/API anahtarı, günlük ve gelişmiş model, araç bazlı model yönlendirmesi, web araması ve kullanım limitlerini panelden yönetir. Kaydetmeden değişiklik uygulanmaz. Anahtarları Slack'e isteme.")
	add("custom_apps", "Entegrasyonlar → Özel bağlantıları yönet", h.connectors != nil, true, "Yönetici HTTPS MCP sunucusunu ve Bearer anahtarını panelde tanımlar, araçları keşfeder, kullanılacak araçları seçip etkinleştirir ve ortak servis hesabını onaylar. Mevcut ext_mcp_* araçları kullanılabilir; bir REST URL veya OpenAPI dosyası doğrudan MCP bağlantısı değildir. Sunucunun Nudgy'den erişilebilir olması ve dağıtımın ağ politikasına uyması gerekir. Gizli ağ adresleri gerekiyorsa operatör dokümante edilmiş sunucu ayarlarını yapar.")
	add("permissions", "Araç izinleri", h.actions != nil, true, "Yönetici dış sistemlere yazan araçları Onay iste, Otomatik veya Kapalı olarak ayarlar. Bekleyen Slack onay kartı işlemin çalıştığı anlamına gelmez. Kapalı bir işlemi başka araçla aşma.")
	_, privacy := h.store.(personalDataStore)
	add("data", "Verilerim", privacy, false, "Kişisel veriler JSON olarak indirilebilir. Geçmişimi ve bağlantılarımı sil bölümü açık SİL onayı ister; geri alınamaz. Konuşmalar, görevler, kişisel bağlantılar ve yerel kayıtlar silinir; Slack/sağlayıcılardaki içerik ve ortak yönetici ayarları silinmez. Slack'te silme veya dışa aktarma aracı yok; kullanıcı panelde tamamlar.")
	add("audit", "Denetim kaydı", privacy, true, "Yönetici güvenlikle ilgili denetim kaydını panelden inceleyebilir; işlem adımları için Canlı işlemler kullanılır.")
	g.Limitations = []string{"Bu rehber hiçbir ayarı değiştirmez, hesap bağlamaz, DM göndermez veya kişisel içerik okumaz.", "RSS okuma/özetleme rss_inventory ve rss_articles; kayıtlı bağlantıları okuma bookmarks_list ile yapılır. Araç hatası boş liste değildir.", "Yerleşik araçlar yeni URL kaydetmez, Feedbin'de okundu işaretlemez, isteğe bağlı saatli görev oluşturmaz veya arka planda işlem takip etmez. İstenen işlemi yapan ayrı bir yetkili araç yoksa desteklenmediğini açıkla.", "Kod/PR/MR ve Jira/Trello işlemleri yalnızca bu isteğe verilen araçlarla yapılır. Mevcut kod düzenleme araçları test/shell çalıştırmaz; başarılı test sonucu uydurma."}
	return g, nil
}
