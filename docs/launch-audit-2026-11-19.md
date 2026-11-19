# LinkedIn duyurusu öncesi proje denetimi — 19 Kasım 2026

İnceleme `1729544` üzerine bu belgeyle birlikte gelen değişiklikleri kapsar.
Yerel `.env` dosyaları ve gerçek hesaplar kullanılmadı. Veritabanları, sağlayıcı
yanıtları, kimlikler ve anahtarlar test için oluşturuldu. GitHub'a push, release
yayımlama veya gerçek Slack'e mesaj gönderme yapılmadı.

## Bulunan ve düzeltilen sorunlar

1. **Başlangıç hatası başarı olarak bildiriliyordu.** HTTP portu doluyken sunucu
   dinlemeye başlayamamasına rağmen çıkış kodu 0 dönüyordu. Hata artık kapanış
   temizliği tamamlandıktan sonra üst seviyeye taşınıyor ve başarısız çıkış
   üretiyor. Yeni subprocess testi düzeltmeden önce başarısız, sonra başarılı
   oldu. Başlangıç logu da henüz açılmamış portu “dinliyor” diye bildirmiyor.
2. **Dar mobil ekranda yatay taşma vardı.** 320 px genişlikte genel bakış,
   canlı işlemler ve kullanım sayfaları 357 px'e taşıyordu. Sayaçların grid
   minimumları düzeltildi; 480 px ve altında tek sütuna geçiliyor. Düzeltmeden
   sonra 12 panel sayfasının tamamında sayfa genişliği 320 px kaldı; açılmış
   istek ve model çağrısı ayrıntısı da taşmadı.
3. **Docker temel imajında yamalanabilir OpenSSL bulguları vardı.** Yüksek/kritik
   tarama temizken tüm-seviye taraması `libcrypto3` ve `libssl3` 3.5.9-r0 için
   toplam 10 orta seviyeli paket bulgusu (5 ayrı CVE) gösterdi. Dockerfile en az
   3.5.9-r1'i gerektiriyor. Yama sürümü Alpine'in
   [resmî güvenlik veritabanından](https://secdb.alpinelinux.org/v3.24/main.json)
   ve paket indeksinden doğrulandı. Yeniden taramada tüm seviyelerde **0 bulgu**.
   CI artık orta seviyeli bulguları da engelliyor.

## Test ve paketleme iyileştirmeleri

- `make check` Go testlerini önbelleksiz çalıştırır; hem `make test` hem
  `make check` kurulum yardımcısını da sınar.
- `make check-integration`, geçici veritabanı ve native test executable'ı
  verilmemişse hata verir. CI bu komutu kullanır; eksik önkoşulların sessiz
  test atlamalarıyla başarılı görünmesi önlenir.
- Migration dosyalarının eksiksiz kaydı, dört eşzamanlı başlangıç, yeniden
  çalıştırmada veri koruma, legacy bootstrap, checksum hatasında geri alma ve
  başarısız boş kurulumun atomikliği için beş test eklendi.
- [Slack görsel paketi](../assets/slack/README.md): 3 PNG ve 11 GIF, önizlemeler,
  Türkçe/İngilizce kullanım yönergesi ve lisans bağlantısı. Yedi hareketli
  emoji 128 × 128, 6–9 kare ve 44 KB altındadır. Büyük mesaj GIF'leri ayrı
  adlandırılmıştır. Kullanıcının verdiği görseller değiştirilmeden kopyalandı.
- Binary paketleme betiği görsel paketini ve SHA-256 karşılıklarını da ekler.
  Yerel görsel üretim kaynakları `/output/` altında korunur ve Git'e girmez.
- README ve güvenlik bağlantıları güncel `BanuKarakaya/Nudgy` adına taşındı;
  görseller ve bu rapor kurulum belgelerinden erişilebilir.

## Doğrulama sonuçları

| Kontrol | Sonuç ve sınır |
| --- | --- |
| Go, yarış denetimi | İlk önbelleksiz JSON turunda alt testler dahil 998 başarılı test kaydı; 3 ücretli model testi ve manuel panel önizlemesi atlandı. Ardından eklenen 7 test kaydı da geçti. Son `make check-integration` ve gerçek release paketleme akışı tüm Go paketlerinde başarılı. |
| Kapsam ölçümü | İlk JSON turunda statement coverage %68,3. Bu, değişiklik öncesi ölçümdür; tüm olası davranışların kapsandığı anlamına gelmez. |
| Statik analiz | `go vet`, Staticcheck 0.7.0, `gofmt`, `git diff --check`, `go mod verify` geçti. |
| JavaScript | 15 test geçti: oturum kapanışı, geç yanıtlar, XSS kaçışı, filtreler, polling ve kayıt durumları. |
| Kurulum yardımcısı | Compose dahil 5 test geçti: bağımsız anahtarlar, 0600 izni, dosya/symlink koruma, özel portlar, harici DB URL'sini koruma. |
| Gerçek PostgreSQL | PostgreSQL 17 üzerinde repository, şirket/üye izolasyonu, kuyruk ve migration testleri geçti. |
| Tekrarlı kuyruk | Yarış denetimli 30 tur: 12.000 iş, 24.000 yinelenen giriş denemesi, 1.800 teslim yeniden denemesi; 82,4 saniye. Gerçek AI/Slack yük testi değildir. |
| Temiz Docker kurulumu | Yayımlanabilir kaynak kopyasından yeni `.env`, boş volume ve özel portlarla uygulama/veritabanı sağlıklı açıldı. Non-root `node`, init ve Codex 0.164.0 doğrulandı. İlk imaj indirmesi ağ zaman aşımına uğradı; doğrudan pull ve tekrar kurulum başarılı. |
| HTTP | 19 kontrol ve 10 paralel bağlantıyla 100 sağlık isteği geçti. Oturumsuz panel, statik görseller, güvenlik başlıkları, kapalı eski API'ler, yetkisiz canlı veri, eksik Slack yapılandırması, `.env`/`.git` erişim reddi kapsandı. |
| Kesinti / yeniden başlatma | 19 migration; DB durduğunda sağlık 503, geri geldiğinde 200. Uygulama yeniden başlatıldıktan sonra test satırı ve migration sayısı korundu. |
| Harici PostgreSQL | Ayrı Compose projesi yalnız uygulama başlattı; mevcut test DB'sine bağlandı; panel/sağlık 200 ve mevcut veri korundu. |
| Native runtime | Pinlenmiş Codex ile macOS testleri; yamalı Linux arm64 imajında model devri, araç çağrısı, rollout kurtarma, 401/429/yarım akış/timeout sonrası toparlanma, gözlem ve kullanıcıya özel durum testleri geçti. Model API'leri yerel sentetiktir. |
| Tarayıcı | 12 panel sayfası masaüstü 1280 px, mobil 390 px ve dar mobil 320 px'te incelendi. Son 320 px ve masaüstü turunda taşma yok; kırık görsel ve gözlenen konsol hatası yok. Yönetici görünümü sentetik önizlemedir; gerçek Slack giriş testi değildir. |
| Go güvenliği | govulncheck 1.9.0: bulgu yok. |
| Gizli bilgi | Gitleaks 8.30.1: 148 commitlik geçmiş ve yayımlanabilir kaynak kopyası temiz. |
| İmaj güvenliği | Trivy 0.76.0: yamalı Linux arm64 imajında tüm önem seviyelerinde 0 bulgu. Ignore listesi veya `--ignore-unfixed` kullanılmadı. |
| Release paketi | Test etiketi `v0.0.0` ile Linux amd64 ve arm64 binary'leri, 18 lisans/NOTICE dosyası ve Slack görselleri üretildi. SHA-256 doğrulaması geçti. Bu yayımlanmış bir sürüm değildir. |
| Görsel dosyaları | 11 GIF'in tüm kareleri açılıp okundu; kaynaklarla byte karşılaştırması yapıldı. Ayrı ZIP açılabilirlik ve içerik karşılaştırmasından geçti. |
| Belge bağlantıları | Depodaki yerel Markdown dosya bağlantıları kontrol edildi; eksik hedef yok. |

Taranan son Docker image ID:
`sha256:959fcacca7be1c33e89f7782aecb2bf33f12597e2080e9c601f8d57269f1cf0f`.

## Yeniden çalıştırma

Gereksinimler ve veritabanı izolasyonu için [CONTRIBUTING](../CONTRIBUTING.md)
ve [self-hosting](self-hosting.md) belgelerine bak.

```sh
NUDGE_TEST_DATABASE_URL='postgres://nudge:nudge@localhost:5432/nudge_test?sslmode=disable' \
NUDGY_TEST_CODEX_BINARY='/absolute/path/to/pinned/codex' \
NUDGY_TEST_COMPOSE=1 make check-integration
make lint
make build
```

Veritabanı kesinlikle test için ayrılmış olmalı. Native executable sürümü
Dockerfile ile aynı olmalı. Normal testler ücretli model çağrısı yapmaz.

## Kalan gerçek ortam kabulü

Bu çalışma hatasızlık garantisi değildir. Yeni bir gerçek Slack workspace'inde
uygulama oluşturma, OAuth onayı, kullanıcı katılımı, olay teslimi ve gerçek
sağlayıcılarla uçtan uca kullanım bu turda yapılmadı. Ücretli model testleri
çalıştırılmadı; önceki tarihli canlı testler bu turun sonucu olarak sayılmadı.
GIF'lerin gerçek workspace'e yüklenmesi de kullanıcıya bırakıldı.

Docker çalıştırma/tarama platformu Linux arm64'tür; amd64 binary derlendi fakat
bu turda ayrı amd64 container çalıştırılmadı. CI'ın uzak sonucu, bu yerel
kontrollerle aynı şey değildir. Kod incelemesi öncelikle kurulum, yetkilendirme,
tenant izolasyonu, kuyruk, migration, hata yönetimi ve arayüz sınırlarına
odaklandı; bağımsız penetrasyon testi veya üretim kapasitesi ölçümü yapılmadı.

Ham kanıtlar Git'e alınmayan `.local/launch-audit/` altında tutulur:
`go-tests.jsonl`, `coverage.out`, `final-check.log`, `bind-before.log`,
`bind-after.log`, `migrations.log`, `queue-soak.log`, `docker-patched.log`,
`http-smoke.json`, `restart-recovery.json`, `external-check.json`,
`native-linux.log`, `browser-checks.json`, `mobile-fixed.jpg`,
`govulncheck.log`, `gitleaks-history.log`, `gitleaks-source.log`,
`trivy-final.json` (bulgulu ilk tüm-seviye taraması), `trivy-patched.json`,
`release-package.log` ve `release-checksums.log`.
