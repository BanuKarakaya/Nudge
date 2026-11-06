package service

import (
	"errors"

	"nudge/internal/coding"
)

// Only host-defined categories cross into the model/logs. Never return the
// original error: provider/network/database diagnostics can contain secrets.
func AgentToolFailure(err error) (code, message string) {
	switch {
	case errors.Is(err, coding.ErrMergeRequest):
		return "merge_intent_required", "Merge yapılmadı: mevcut Slack mesajı bu PR/MR için açık merge komutu olarak doğrulanamadı. Kabul edilen biçim: <PR/MR URL> merge et. Bu bir komut doğrulama hatasıdır; GitHub/GitLab izin veya branch protection hatası olduğunu söyleme. Aynı parametreleri tekrar deneme; kullanıcıya doğru komutu göster."
	case errors.Is(err, coding.ErrBranchMerge):
		return "branch_merge_intent_required", "Merge yapılmadı: kaynak/hedef dal isteği veya thread’deki görev belirsiz. Açık biçim: github owner/repo source -> target merge et (GitLab için gitlab group/project source -> target merge et). Kullanıcıya gerçek repo ve dallarla bu komutu göster. Bu bir komut/görev doğrulama hatasıdır; provider izin hatası değildir. Aynı parametrelerle tekrar deneme."
	case errors.Is(err, coding.ErrChangeMoved):
		return "merge_revision_changed", "Merge yapılmadı: PR/MR kaynağı, hedefi veya kayıtlı hesap/görev eşleşmesi değişmiş. Güncel durumu göster; değişen hedef veya revizyon için yeni bir açık kullanıcı isteği gerekir."
	case errors.Is(err, coding.ErrMergeBlocked):
		return "merge_blocked", "Merge tamamlanamadı: PR/MR durumu veya provider kontrolleri birleştirmeye izin vermedi. Durumu sorgula ve yalnızca doğrulanmış nedeni açıkla; belirli bir izin, onay veya CI eksikliği uydurma."
	case errors.Is(err, coding.ErrAPIScope):
		return "gitlab_api_scope_required", "GitLab bağlantısı api izniyle yeniden yetkilendirilmeli. gitlab_connect aracını kullan."
	case errors.Is(err, errAgentArguments), errors.Is(err, coding.ErrArguments):
		return "invalid_arguments", "Araç parametreleri geçersiz. Araç şemasını ve doğrulanmış repo/dal/görev bilgilerini kontrol et; aynı hatalı parametreleri tekrar deneme."
	default:
		return "tool_failed", "Tool failed. Do not invent data or interpret this as an empty result. The user may need to check their connection or retry. The cause is unconfirmed; do not assume branch protection or missing permissions."
	}
}
