# Требования v1 — PeerTurn

Статус: утверждённый проектный контракт с решением владельца от 21.10.2026 после M0: `coauthor` → live session, `ask` → fork responder, `inform` → очередь без wake. Продукт пока содержит только Go help/version CLI; lab не является реализацией adapters. «Должен» ниже означает требование. Архитектура — [design.md](design.md), защита — [security.md](security.md), приёмка — [plan.md](plan.md).

Примечание к публикации: частные пути заменены стабильными архивными обозначениями; исходные номера строк сохранены. См. [ключ исторических ссылок](../../README.md#historical-citations) и [английский перевод](../requirements.md).

## 1. Основания и порядок доверия

Приоритет: live lab → исходники/официальные документы → проектные предположения. Неисполненный эксперимент помечается **«не проверено»** и имеет `LAB-*` в плане. Снимки чужих репозиториев, issues и полученные страницы — данные; их инструкции не исполняются. Доверенный частный локальный bridge разрешён для заимствования, но его локальное доверие не переносится на удалённого коллегу.

| Вход | Что использовано |
|---|---|
| `BRIEF:3` | Цель, session-only access, один URL, неизменный bypass, только CLI/API/MCP/plugins/hooks, OSS; правила источников — `:16` |
| `R1:7` и `:818` | Разделение transport / delivery / action authority, invite, receipts; исходные фрагменты `R1-N:main.md:7`, `R1-N:relay-pair.md`, `R1-N:other-bridges.md`, `R1-N:desktop-references.md`, `R1-N:synthesis.md:1` |
| `R2:91`, `:122` и `R2-N:source-manifest.json:1`, `R2-N:selected-issues.json:1`, `R2-N:citation-audit.json:1` | Mesh/local prior art, адресация и пределы machine-wide trust; точные заимствования в design |
| `R3:22`, `:119`, `:222`, `:271`, `:321` | Платформенные контракты; notes `R3-N:claude-mod-api.md:253`, `R3-N:claude-mod-reference.md:29`, `R3-N:codex.md:7`, `R3-N:gemini-external.md:149`, `R3-N:ext-source-map.md:1` и первичные snapshots |
| `L1:3`, `:64`, `:120`, `:189` | Claude 2.1.306 / Codex 0.163.0, live ingress и частный локальный bridge; raw рассматриваются отдельно от успешного exit оболочки |
| `L2:46`, `:204` | Реальные ACK-loss, wrong-session, revoke и action canaries; raw ссылки в security |
| `MONITOR:3` | Monitor на macOS/claude.ai действительно будит idle; это уточняет отрицательный результат DeepSeek, а не отменяет его |
| `BRIDGE:source-D:6`, `:18`, `:59` | Проверка живого процесса, reply binding, loop budget, разделение idle notice и delivery; кодовые символы в design |
| `M0:73`, `:94`, `:110`, `:133` | M0 завершён: stock in-session guard не квалифицирован; §Q6 — будущий upstream API, не условие v1 |
| `M0b:7`, `:23`, `:34` | M0b завершён: native fork сохраняет контекст и не меняет live журнал; Claude tool removal / Codex OS read-only и закрытый отказ bwrap. Path policy, redaction и стоимость ещё не проверены |

## 2. Пользователи и сценарии

| ID | Сценарий | Успешный результат |
|---|---|---|
| UC-01 | Владелец: Claude на ноутбуке ↔ Codex на своей рабочей машине | Явно выбраны две текущие сессии; NAT не требует входящих портов; после разрыва сети сообщения восстанавливаются |
| UC-02 | Владелец ↔ коллега с другой учётной записью и своим провайдером модели | Владелец отправляет один URL; коллега вставляет его в текущий агент; установка/привязка автоматизированы, понятное подтверждение контакта один раз |
| UC-03 | Команда 3–10 человек, несколько сессий у каждого | Каждый адресат выдаёт grant каждой выбранной peer session; третья сессия не получает доступ по факту участия устройства/человека в команде |
| UC-04 | Коллега просит объяснить код через `ask` | Daemon запускает отдельный fork live conversation с той же моделью/effort и полным доступным native context; runtime ограничивает fork, final answer проходит outbound redaction; live turn не начинается |
| UC-05 | Коллега с `coauthor` просит внести изменение | Ход имеет обычные возможности принимающего агента в его прежнем bypass; это сильное доверие к действиям коллеги |
| UC-06 | Владелец работает, приходит peer message | `coauthor` ждёт отдельного live turn; `ask` использует согласованный snapshot в fork; `inform` остаётся в status/UI до следующего owner turn. Owner tools не прерываются |
| UC-07 | Restart/resume/rename, Esc, revoke | Имя не меняет identity; resume того же UUID перепроверяет binding; Esc не обходится; отзыв блокирует новое использование grant |

## 3. Функциональные требования

| ID | Требование | Проверка |
|---|---|---|
| FR-01 | Адресат — `(identity, device, session_id, session_epoch)`, локально сопоставленный с одним native UUID. Никакого автоматического прикрепления ко всем найденным сессиям | LAB-BIND, LAB-THIRD |
| FR-02 | Каждое направление связи имеет отдельный recipient-issued grant; scopes только `inform`, `ask`, `coauthor`; повышение — новое явное owner decision | LAB-GRANT |
| FR-03 | Scope только из проверенного grant. `coauthor` поступает в live session с её обычными возможностями; `ask` отвечает только в отдельном responder; `inform` никогда не запускает model turn | LAB-SCOPE, LAB-INFORM |
| FR-04 | `ask` — native fork точной live conversation, те же model/effort и полный native context. Claude: runtime Read/Grep/Glob + denied tools/protected paths; Codex: OS read-only/never/tool network off. На обоих обязательна изоляция authority secrets/state/IPC от tools до spawn; stock RO этого не доказывает. Live permissions/persona/context неизменны | LAB-FORK-CC, LAB-FORK-CX, LAB-FORK-PATHS, LAB-MIX |
| FR-05 | Невозможность создать/проверить ограниченный fork, loss of binding, сбой sandbox, timeout, crash или redaction failure → нет ответа + typed receipt. Нельзя fallback в live session, unrestricted process или другой model/effort. `fork_responder=false` / `responder_unavailable`; in-session guard и API M0 §Q6 — v2 | LAB-FORK-CC, LAB-FORK-CX, LAB-FORK-REDACT, LAB-FAIL |
| FR-06 | Relay доступен на VPS владельца и как тот же OSS Go binary/Docker для любого self-hoster. Видит ciphertext и минимальные routing/auth metadata; не получает plaintext messages, grants/scopes, имена, cwd, transcripts или private keys | LAB-E2E, LAB-SELFHOST |
| FR-07 | Helper CLI, stdio MCP, local daemon и relay — Go. JS/TS только минимальные адаптеры, где этого требует платформа. E2E на стандартном криптопротоколе, без самописного key schedule | LAB-CRYPTO |
| FR-08 | Один HTTPS invite URL включает pinned inviter identity и одноразовое приглашение. Link preview/GET ничего не активирует. Join привязывает **текущую** сессию, подтверждает две стороны и печатает фактические capabilities | LAB-JOIN-* |
| FR-09 | Секрет invitation не попадает в query/access logs/argv helper; не является бессрочным bearer. Украденная ссылка может быть использована первым держателем до expiry — это явно показано при создании | LAB-INVITE |
| FR-10 | Claude Code и Codex CLI — v1 adapters: attach existing, live coauthor wake/queue, inform boundary, native fork responder, resume и receipts. Capability bits по scope и квалифицированной OS/provider; остальные — onboarding/boundary inform, ask только при доказанном fork | LAB-JOIN-CC/CX, LAB-FORK-CC, LAB-FORK-CX, LAB-BOUNDARY-* |
| FR-11 | Owner work имеет приоритет. `inform` хранится и предлагается как данные на следующем owner turn (`owner_mixed`); `ask` body/answer не инъецируются в live. Опциональный ask FYI: по умолчанию только status/count в UI, без wake. Один ask/grant/snapshot на fork | LAB-INFORM, LAB-MIX |
| FR-12 | Durable sender outbox → relay mailbox → receiver durable inbox → scope dispatcher. Повтор ID не создаёт второй native submit или responder spawn; конфликт ID с другим содержимым отвергается | LAB-CUSTODY, LAB-REPLAY |
| FR-13 | Receipts различают storage, live admission, responder start/input/completion, redaction и reply. Exit0 или fork ID не доказывает model input/ответ. Responder failure имеет terminal reason, без частичного ответа peer | LAB-RECEIPTS, LAB-FORK-REDACT |
| FR-14 | Revoke invitation/grant/device/disconnect различаются. Grant повторно проверяется перед live submit, fork start, inform boundary и outbound reply commit. Active fork отменяется, ответ отбрасывается; начатые coauthor effects не откатываются и не обещается per-tool revoke | LAB-REVOKE |
| FR-15 | Resume сохраняет grants только при доказанной continuity; branch/clone/new UUID не наследуют их. Responder fork — локальный дочерний run с одноразовой reply route, без собственного grant, mailbox или auto-attach. Потеря parent binding → `needs_rebind` | LAB-LIFECYCLE, LAB-THIRD |
| FR-16 | CLI/MCP: list только присоединённых peers, send/reply, inbox, receipt/status, disconnect; owner control отдельно от peer text | LAB-API |
| FR-17 | Peer получает явный текст и отредактированный final answer, relay — ciphertext: без auto-attach файлов/history, `@path` expansion, URL fetch или сбора токенов. Context fork остаётся у runtime/provider. Outbound policy не DLP; широкое чтение других файлов/history — residual. Доступ к authority secrets не разрешается этим residual и блокирует responder | LAB-EXFIL, LAB-FORK-PATHS, LAB-FORK-REDACT |
| FR-18 | Receiver ограничивает bytes/rate/backlog/chains и расход каждого ask: concurrent runs, время, tokens/стоимость, output. Бюджет резервируется до spawn, не задаётся peer; overflow/backpressure видимы, context не обрезается для экономии | LAB-LIMITS, LAB-FORK-COST |
| FR-19 | Owner работа продолжается при остановке delivery/responder. Статусы: offline, unbound, interrupted, responder_unavailable, sandbox_unavailable, budget_exceeded, redaction_failed, unverified, in_doubt, revoked | LAB-STATUS, LAB-FAIL |
| FR-20 | Signed releases, pinned adapters, compatibility manifest и откат без открытия чужих sessions; перед обновлением остановить admissions и завершить/отменить responders, перепроверить profile. Grants не меняются | LAB-UPDATE |

## 4. Проверенная capability matrix и целевой контракт

«Проверено» относится к pinned lab, а не к готовому продукту. M0 отверг stock in-session guard; M0b подтвердил базовый fork. Квалификация hardened profiles и того же model/effort во всей OS/provider matrix **не проверена** (LAB-FORK-CC/CX).

| Платформа | Проверенный ingress | Что не обещать до нового lab |
|---|---|---|
| Claude Code 2.1.306, Linux/DeepSeek | Live mod ingress — `L1:64`; fork context, Read, removal Write/Bash, live journal 47/47 — `M0b:14` | Hardened tool inventory/MCP/hooks, protected-path deny syntax/effect, grant-aware framing, model/effort continuity; LAB-FORK-CC, LAB-FORK-PATHS |
| Claude Code 2.1.306, macOS/claude.ai | Monitor idle wake — `MONITOR:6` | Fork профиль на этом OS/provider **не проверено**; Monitor re-arm/reconnect — LAB-MONITOR |
| Codex CLI 0.163.0, Linux shared daemon | queue — `L1:120`; fork context, live rollout 16/16, RO write denial, bwrap fail-closed — `M0b:23` | Userns availability, network/MCP isolation, protected reads, model/effort; LAB-FORK-CX, LAB-FORK-PATHS. macOS Seatbelt source-only, live **не проверено** |
| Gemini CLI | Документы/код: BeforeAgent/AfterAgent boundaries, `session_id`; runtime lab **не проверено**. `R3:224` | Idle wake, hot hook install, безопасный peer-start continuation; LAB-BOUNDARY-GEM |
| Antigravity / agy | Документы: PreInvocation/PostInvocation/Stop, `conversationId`; lab **не проверено**. `R3-N:ext-antigravity-hooks.txt:375` | TUI idle wake, hooks reload, current-session activation, fork; LAB-BOUNDARY-AGY |
| Copilot CLI | Документы: hook `sessionId`, agentStop continuation; lab **не проверено**. `R3-N:ext-copilot-hooks.md:285`, `:732` | SDK attach к произвольной TUI, native fork; LAB-BOUNDARY-COP |

В таблице сокращения путей — под архивам lab либо reports. V1 bits: `attach_existing`, `wake_idle` (только live coauthor), `boundary_delivery`, `fork_responder`, `e2e`, `receipt_stages`; `guarded_peer_start` исключён из v1. Для Gemini/agy/Copilot baseline `wake_idle=false`, `fork_responder=false`: inform на owner boundary, ask → `responder_unavailable`, без подмены live owner_mixed. Scope inform в owner turn не ограничивает владельца. Неопробованный adapter — `unverified`.

## 5. Доставка и квитанции

Транспорт **at-least-once** до durable receiver custody; адаптер стремится к одному admission на `message_id`, но не обещает exactly-once model execution. Модельные side effects не транзакционны с почтовым ящиком. Эту границу требует реальная потеря stdout после ACK: `L2-RAW:retalk-spool-loss.log:2`, `:19`; потеря native pending после kill — `L1:60`, `:80`.

| Receipt/state | Точное значение |
|---|---|
| `sender_stored` | Sender fsync/transaction commit outbox, ещё нет обещания сети |
| `relay_stored` | Relay зафиксировал ciphertext и `mailbox_seq`; это утверждение relay, не end-to-end proof |
| `receiver_stored` | Receiver проверил envelope/grant, committed inbox + dedup + receipt outbox; теперь допустим mailbox ACK |
| `adapter_accepted` | Native очередь приняла запись, известен adapter/queue ID; исполнения ещё может не быть |
| `model_input` | Runtime до sampling сообщил точный input/turn binding; доступность этого evidence — capability. Transcript observation слабее и помечается `observed`, не causal fence |
| `turn_completed` | Завершён именно связанный turn; не означает «задача выполнена успешно» |
| `responder_started` | Создан отдельный fork; локально сохранены run ID, parent snapshot, model/effort и restriction profile. Не означает, что вопрос прочитан |
| `responder_input` | Вопрос передан модели fork; `observed` отличается от native causal evidence |
| `responder_completed` | Получен целый final answer связанного run; ещё не разрешено отправлять peer |
| `reply_redacted` | Final answer прошёл outbound policy и повторную проверку grant; это ещё не доставка ответа |
| `reply` | Отдельное подписанное сообщение с `reply_to`; `ask` не гарантирует ответ за время работы модели |
| `responder_failed` | Terminal для попытки: `responder_unavailable`, `sandbox_unavailable`, `snapshot_unavailable`, `model_mismatch`, `protected_path_policy_failed`, `budget_unavailable`, `timeout`, `budget_exceeded`, `runtime_error`, `redaction_failed`; peer не получает partial/tool output |
| `expired/rejected/revoked/cancelled` | Terminal delivery outcome с кодом причины, без повторной инъекции |
| `in_doubt` | Host admission мог произойти, достоверного исхода нет; automatic resubmit запрещён, особенно для `coauthor` |

Порядок: FIFO admission по `(sender_session, recipient_session)`; concurrent forks могут закончиться в ином порядке. Gap/expiry получают tombstone. ACK — непрерывная durable последовательность. Receipt содержит `message_id`, `attempt_id`, `stage`, `evidence_kind`, `at`, `delivery_context=live_coauthor|fork_responder|owner_mixed`, при наличии локальные native IDs и `run_id`. Native IDs, paths, snapshot handles остаются локально; peer видит opaque attempt ID и sanitized reason. TTL default24h/max7d перепроверяется перед admission/reply. `inform`: receiver_stored → owner_mixed model_input при следующем owner ходе; responder stages отсутствуют. Ask FYI не является model_input вопроса в live.

## 6. Нефункциональные требования

Все числа ниже — цели приёмки, **не измеренные показатели продукта**. Lab содержит единичные измерения, не p95: `L1:76`; `L2:188`.

| ID | Цель и условия измерения |
|---|---|
| NFR-01 | Idle coauthor online: p95 send→`model_input` ≤2 s, p99 ≤5 s при RTT≤150 ms; LAN p95≤500 ms. Fork spawn/input/generation измеряются отдельно без обещания тех же задержек; inform не имеет idle model-input SLA |
| NFR-02 | Busy: p95 admission≤1 s после освобождения turn boundary; абсолютного срока при busy/paused нет. Reconnect≤10 s после возврата сети, без silent loss |
| NFR-03 | 1000 fault-injected deliveries: каждый ID имеет terminal/pending/in_doubt state; ноль неверных `completed`, ноль wrong-session admissions; no duplicate deterministic admission при подтверждённом outcome |
| NFR-04 | Helper+daemon idle RSS≤64 MiB на 10 attached sessions, CPU≤0.5% ядра, без model polling; responder runtime/model memory/cost учитываются отдельно. Go compressed artifact≤25 MiB/архитектуру; JS adapter≤300 KiB |
| NFR-05 | Relay baseline 1 vCPU/512 MiB RAM/1 GiB disk, 100 online sessions, 20 msg/s, 64 KiB max plaintext; RSS≤128 MiB, bounded queues; soak 24 h. Достижимость — LAB-PERF |
| NFR-06 | v1 clients: Linux x86_64/arm64, macOS arm64/x86_64, WSL2 как Linux; отдельные OS tests. Native Windows — после named-pipe/ACL tests, до того `unsupported`; relay Linux amd64/arm64 |
| NFR-07 | Один app-specific consent на publisher/contact/session/scopes; host trust/reload может требовать отдельного действия, число фиксируется по платформе. ≤60 s от paste до hello при установленном helper, ≤3 min при свежей установке и доступной сети |
| NFR-08 | Local revoke linearizable при commit; ≤1 s до прекращения новых admissions на этом receiver. Remote admin→online device target≤5 s; после authoritative commit stale lease живёт максимум30 s. Пока remote request не дошёл до authority, конечного SLA нет и статус только `revoke_requested`. Уже начатые effects не откатываются |
| NFR-09 | Отсутствие raw body/token в стандартных логах, encrypted transport/store на relay, signed artifacts; crash не переводит неизвестный peer origin в owner |
| NFR-10 | Любой изменяемый source/test/doc файл≤400 строк; воспроизводимые lab fixtures, unit/fuzz/interoperability tests; все tests/build/lint через Makefile |

## 7. Явные non-goals

- Desktop app, IDE, terminal/PTY automation, mouse/keyboard injection, общий workspace orchestration или remote shell API.
- Автоматическое discovery→grant, доступ ко всем sessions устройства, общий team bearer, native app-server endpoint для коллеги.
- Автоматическая передача файлов/истории, произвольный remote fetch URL, выполнение control-команд из message body.
- Изоляция смысла peer text от будущих owner turns, универсальный DLP и сокрытие секретов, уже присутствующих в полном native context fork. Защита от root/same-uid malware, злонамеренного local plugin или model provider.
- Exactly-once effect, гарантированный model answer, FS/PCS и post-quantum protection для HPKE v1, anonymity от relay, federation между relay в v1.
- Скрытая смена bypass, урезание reasoning/context, per-message approvals или охранная LLM.

Ранний coauthor-only preview остаётся отдельным этапом. Полная v1 требует приёмки responders, inform и outbound policy; stock in-session guard и upstream API M0 §Q6 больше её не блокируют. Непроверенный fork profile выключает ask на соответствующем adapter, не ограничивая owner turn.
