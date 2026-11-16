# Yayın kontrolleri — 16 Kasım 2026

Bu belge önceki incelemelerin yerine tüm özellikler için hatasızlık iddiası
koymaz; yeni kontrollerin kanıtını ve kalan kabul adımlarını kaydeder.

## Tamamlanan değişiklikler

- `scripts/release.sh` Linux amd64/arm64 binary'lerinin yanına MIT lisansını,
  güncel changelog'u, `THIRD_PARTY_NOTICES.md` ve `LICENSES/` altındaki 18 lisans ve
  NOTICE dosyasını koyar. SHA256SUMS hem binary'leri hem bu dosyaları kapsar.
- Changelog; MCP bağlantıları, canlı gözlem/kullanım ekranları, doğal dil panel
  erişimi, kişisel Feedbin/Raindrop yenileme ve yeni kontrolleri kapsar.
- Eski tarihli incelemeler güncel durumdan ayırt edilir ve bu belgeye bağlanır.
- Normal CI'a gerçek PostgreSQL yük/bağlantı kaybı, native sağlayıcı kesintisi ve
  şirket kurulumu yönetici/üye izolasyonu regresyonları eklendi. Ücretli doğal
  dil testi ayrıca `make eval-language` ile çalıştırılır; otomatik CI bunu çalıştırmaz.

## Doğrulama

| Kontrol | Kapsam / sonuç |
| --- | --- |
| Gerçek model doğal dil kabulü | 19 senaryo × 2 model = 38 başarılı kontrol; DeepSeek `deepseek/deepseek-v4.1-flash-1017` ve `openai/gpt-6-luna`; 73 HTTP model isteği; yaklaşık 175 saniye |
| Dil kontrolünün sınırı | Üretimdeki native runtime, araç şemaları ve rehber kullanılır. DM, RSS ve hesap yan etkileri sentetiktir. Beklenen araçlar/DM denemeleri ve sınırlı cevap kavramları doğrulanır; tüm ifadelerin veya özelliklerin doğruluğunu kanıtlamaz. |
| Şirket/üye kurulumu | Gerçek HTTP sunucusu, cookie jar ve PostgreSQL; iki şirket, yönetici ve üye; üyenin yönetici sayfalarına/yazma işlemine 403; şirket bazlı iptal. Slack OAuth ve doğrulanmış kimlik girdileri sentetiktir. |
| Kuyruk eşzamanlılığı | Tek turda 400 iş, 800 yinelenen giriş, 20 şirket kapsamlı konuşma, 8 worker ve 60 teslim yeniden denemesi; iş sırası ve tek terminal analitik kaydı doğrulandı. AI/Slack çağrıları içermez. |
| Tekrarlı kuyruk yükü | Aynı yarış denetimli test 300 tur geçti: 120.000 iş, 240.000 yinelenen giriş ve 18.000 yeniden deneme; 342,5 saniye. Bu sentetik kuyruk testi gerçek AI kapasitesi veya günler süren soak testi değildir. |
| Veritabanı bağlantısı kaybı | Testin PostgreSQL bağlantısı zorla sonlandırıldı; havuz yeniden bağlandı; kaydedilmiş yanıt korundu, eski worker'ın yazması reddedildi. |
| Gerçek sunucu/DB yeniden başlatma | Boş test veritabanında 18 migration, dashboard ve sağlık 200. PostgreSQL ani durdurmada sağlık 503; PostgreSQL ve ardından uygulama yeniden başlatılınca 200. Kaydedilen test verisi ve migration sayısı korundu. |
| Model API kesintisi | Gerçek native runtime ve yerel sentetik API: 401, 429, yarım SSE yanıtı ve zaman aşımı. Başarısız çağrı başarıya dönüştürülmedi; sonraki sağlıklı çağrı aynı runtime'da tamamlandı. |
| Kod / dashboard | `make check` (format, vet, Go race ve 15 JavaScript testi) ve Staticcheck geçti. Ücretli testler ayrıca yukarıdaki komutla çalıştırıldı. |
| Kurulum yapılandırması | 5/5 kontrol: bağımsız özel anahtarlar, mevcut dosya/symlink koruması, harici PostgreSQL bağlantısı ve özel portlar. |
| Binary aday paketi | `sh scripts/release.sh v0.1.0` geçti; iki Linux binary'si ve 18 lisans/NOTICE dosyası, içerik ve checksum karşılaştırmasıyla doğrulandı. Bu komut tag veya GitHub release yayımlamaz. |

Yerel PostgreSQL testleri geçici PostgreSQL 14 üzerinde çalıştırıldı. Normal CI
PostgreSQL 17 ve gerçek Docker kurulumunu ayrıca çalıştırır. Yerel Docker daemon
bu oturumda yanıt vermediği için bu çalıştırma yerel Docker başlangıç kanıtı değildir.
İlk uzun test denemesi bilgisayarın diski dolunca kesildi; uygulama hatası olarak
veya başarılı yük testi olarak sayılmadı. Geçici test veritabanı yeniden açılarak
kontroller tekrarlandı. Disk kapasitesi izleme ve veritabanı yedekleri operatörün
kurulumuna dahildir.

Ham yerel sonuçlar Git'e eklenmeyen `.local/release-completion/` dizinindedir.
`language-eval.log` yalnız sentetik prompt/yanıtları ve araç seçimlerini içerir;
gizli anahtarları içermez. Diğer kanıtlar: `resilience.log`, `queue-soak.log`,
`provider-recovery.log`, `company-installation.log`, `installation.log`,
`release-build.log` ve `boot-recovery.json`.

## Hâlâ gerçek ortamda kabul gerektirenler

- Ayrı bir gerçek Slack workspace'i henüz sağlanmadı; kullanıcı bu kontrolün
  beklemesini istedi. Sıfırdan gerçek Slack onayı, kurulum ve çalışan katılımı
  tamamlanmış sayılmaz.
- Her sağlayıcının gerçek hesaplarıyla OAuth bağlama/yenileme/iptal döngüsü,
  kurumsal SSO/izin politikaları ve kota hataları bu sentetik testlerin yerine geçmez.
- Kuyruk yükü, gerçek model eşzamanlı kapasitesi veya günlerce üretim kullanımı
  değildir. Üretim için kendi model/limit/Slack kotasıyla pilot kullanım gerekir.
- İki modelde tek kabul turu, diğer model/sürüm ve her doğal dil ifadesini kapsamaz.
  Model veya sistem talimatları değişince kabul turu tekrarlanmalıdır.

Açık kaynak beta olarak sunulabilir. Bu kontroller tek başına tüm şirketlerde
hatasız çalışma, bağımsız penetrasyon testi veya sınırsız üretim kapasitesi iddiası
oluşturmaz.
