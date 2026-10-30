# Karusel → Plex: Go relay через OpenVPN (Linux / Codex-ready)

Телеканал «Карусель» (веб-эфир) принимается из отдельного VPN-сетевого пространства контейнера и отдаётся Plex напрямую: сервис сам выглядит для Plex как ТВ-тюнер **HDHomeRun** и отдаёт программу передач в формате **XMLTV**. Запись по расписанию делает Plex DVR (нужен Plex Pass). Threadfin больше не нужен.

**Статус:** MVP. Проверено на Mac (Docker Desktop) с VPN владельца: туннель поднимается, поток идёт через relay (h264 1080p + aac, ≈ 7 Мбит/с), программа передач загружается через VPN и совпадает с эфиром по времени. **Не проверено:** подключение тюнера и запись в Plex, показ «Спокойной ночи, малыши!» в 21:00 (не заставка ли в веб-эфире). Проект не обходит DRM и не перепубликует поток в интернет.

## Схема

```text
karusel-tv.ru/live / MediaVitrina (HLS)          karusel-tv.ru (расписание JSON)
                 |                                         |
       VPN tunnel (Gluetun/OpenVPN)  <---------------------+
                 |
     Go relay: ссылка у балансера MediaVitrina -> Streamlink (HLS)
                 |                         |
   /live.ts (MPEG-TS)        /discover.json, /lineup.json, /epg.xml
                 |
   127.0.0.1:18080 на Linux-хосте
                 |
   Plex Live TV & DVR (тюнер HDHomeRun + XMLTV)
```

В VPN работает **только** relay. Plex и остальной хост через VPN не ходят.

## Быстрый старт (Linux)

Нужны Docker Engine, Docker Compose v2, рабочий `/dev/net/tun` и OpenVPN-профиль с разрешённым доступом к эфиру.

```bash
cp .env.example .env
chmod 600 .env
mkdir -p vpn
cp /path/to/your/client.ovpn vpn/client.ovpn
# Сертификаты/ключи — тоже в vpn/, пути в .ovpn поправьте на /gluetun/vpn/...
# Логин/пароль VPN — в OPENVPN_USER/OPENVPN_PASSWORD в .env
bash scripts/deploy.sh   # или: make deploy
```

> **Важно про профиль:** Gluetun не принимает имя хоста в строке `remote` — нужен IP-адрес. Если у провайдера DDNS-имя, разрешите его в IP заранее и обновите профиль, если адрес сменится.

> При первом запуске `deploy.sh` создаст `.env` из шаблона и **остановится**, чтобы вы его проверили.

Не добавляйте `.env`, `.ovpn`, ключи и сертификаты в Git.

Проверка:

```bash
docker compose ps
docker compose logs --tail=100 vpn relay
bash scripts/smoke.sh          # healthz, status, playlist, тюнер, /epg.xml
make smoke-video               # ~20 секунд эфира + ffprobe (на хосте или в контейнере relay)
```

`/healthz` означает только, что Go-сервер отвечает, — он **не подтверждает**, что VPN или источник работают. В `/status` видно число зрителей, перезапуски Streamlink (`upstream_restarts`) и состояние программы передач (`epg`).

## Подключение Plex

Для Plex в Docker с `network_mode: host` на той же машине никаких правок в его compose не нужно: он видит сервис по `127.0.0.1:18080`.

1. Plex → **Настройки → Live TV & DVR → Set up Plex DVR**.
2. Устройство само не найдётся (широковещательный поиск не поддерживается). Выберите **«Don't see your HDHomeRun device? Enter its network address manually»** и введите `127.0.0.1:18080`.
3. Plex увидит один канал — **1 «Карусель»**.
4. Программа передач: **«Have an XMLTV guide on your server? Click here to use that instead»** → `http://127.0.0.1:18080/epg.xml`. Сопоставьте канал `1` с «Карусель», если Plex не сделал это сам.
5. Нужна библиотека «ТВ-передачи», куда Plex будет класть записи.
6. В гиде найдите «Спокойной ночи, малыши!» → **Record → Record Series**. В настройках записи: начинать на 2 минуты раньше, заканчивать на 3 минуты позже; хранить, например, последние 14 выпусков.

Каждый выпуск в программе — отдельная серия с датой эфира (`S2026E1030` = 30 октября 2026): так задумано, чтобы Plex записывал выпуск каждый день и подписывал файлы датой. В Plex это ещё не проверено. Один выпуск (≈ 23 минуты с отступами) занимает ≈ 1.2 ГБ.

Передачи на «Карусели» идут блоками: «Барбоскины» — это почти 4 часа подряд одним блоком, отдельную серию из блока записать нельзя.

Обновление со старой схемы с Threadfin — в [docs/deployment-linux.md](docs/deployment-linux.md#upgrading-from-the-threadfin-setup).

## Локальная проверка на Mac

`compose.local-plex.yaml` добавляет к стеку контейнер Plex для проверки тюнера, гида и записи:

```bash
# 1. Сначала скачать образы и собрать relay: это может занять дольше 4 минут
docker compose -f compose.yaml -f compose.local-plex.yaml pull plex
docker compose -f compose.yaml -f compose.local-plex.yaml build relay
# 2. Только теперь взять PLEX_CLAIM с https://plex.tv/claim (действует 4 минуты) и вписать в .env
make local-up                 # VPN + relay + Plex
open http://127.0.0.1:32400/web
make local-down               # остановить (тома сохраняются)
```

Если сервер в Plex не привязался к аккаунту (токен истёк), возьмите новый `PLEX_CLAIM`, обновите `.env` и снова выполните `make local-up`: claim срабатывает при первом запуске, поэтому может понадобиться `make local-down` и удаление тома `karusel-plex-relay_plex-local-config`.

Отличие от прода одно: локальный Plex обращается к сервису по адресу `vpn:8080` (тюнер и XMLTV `http://vpn:8080/epg.xml`), а не `127.0.0.1:18080`.

## Работа с Codex

Откройте папку как отдельный репозиторий в Codex. Он прочитает [`AGENTS.md`](AGENTS.md); стартовый запрос — [`CODEX_PROMPT.md`](CODEX_PROMPT.md), приоритеты — [`TASKS.md`](TASKS.md).

```bash
make check   # gofmt, vet, race-тесты, проверка compose (без реального VPN)
```

## Структура

```text
AGENTS.md                 Инструкции Codex и технические границы
CODEX_PROMPT.md           Стартовый запрос агенту
TASKS.md                  План развития с acceptance criteria
cmd/relay/main.go         Запуск HTTP-сервера, фоновое обновление программы
internal/relay/           Конфигурация, HTTP API, тюнер HDHomeRun, Streamlink и перезапуски
internal/epg/             Загрузка расписания, кэш, XMLTV
compose.yaml              VPN + relay
compose.local-plex.yaml   Локальный стенд с Plex (только для проверки)
Dockerfile                Multi-stage Go + Python/Streamlink + tini
.env.example              Параметры; реальные секреты — только в .env
vpn/README.md             Как положить OpenVPN-профиль
scripts/                  check, deploy, smoke
docs/                     Архитектура, развёртывание, диагностика
.github/workflows/ci.yml  CI для Go без VPN-секретов
```

## API и параметры

| Метод | URL | Назначение |
| --- | --- | --- |
| GET | `/healthz` | HTTP liveness (не проверяет VPN) |
| GET | `/status` | JSON: зрители, лимит, uptime, `upstream_restarts`, `epg` |
| GET | `/playlist.m3u` | IPTV-плейлист с постоянным URL |
| HEAD | `/live.ts` | Заголовки без запуска процесса |
| GET | `/live.ts` | MPEG-TS; при обрыве источника Streamlink перезапускается в том же ответе |
| GET | `/epg.xml` | Программа передач XMLTV; 503 до первой удачной загрузки |
| GET | `/discover.json`, `/lineup.json`, `/lineup_status.json` | Тюнер HDHomeRun для Plex |
| POST | `/lineup.post` | Запрос сканирования каналов (ничего не делает) |

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `SOURCE_URL` | MediaVitrina player URL | Источник, **может измениться** |
| `PUBLIC_BASE_URL` | `http://127.0.0.1:18080` | Абсолютные URL в M3U и в ответах тюнера |
| `RELAY_PORT` | `18080` | Порт хоста (только 127.0.0.1) |
| `MAX_VIEWERS` | `2` | Тюнеры для Plex = одновременные Streamlink (запись + просмотр) |
| `STARTUP_TIMEOUT` | `45s` | Ожидание первых данных до HTTP 502 |
| `RECONNECT_WINDOW` | `5m` | Сколько перезапускать Streamlink после обрыва посреди ответа |
| `EPG_SOURCE_URL` | `https://www.karusel-tv.ru/schedule/Europe/Moscow` | Источник расписания; часовой пояс — в пути |
| `EPG_REFRESH` | `6h` | Как часто обновлять программу |
| `EPG_DAYS` | `12` | Максимум дней вперёд (сайт отдаёт ~10) |
| `OPENVPN_USER/PASSWORD` | пусто | Авторизация OpenVPN при необходимости |
| `PLEX_CLAIM` | пусто | Только для локального стенда |

Память (замер на Mac): `vpn` ≈ 40 МиБ, `relay` ≈ 16 МиБ в простое и ≈ 60 МиБ во время потока.

Ограничения: URL источника и формат балансера MediaVitrina могут измениться; на каждого зрителя — отдельный Streamlink; при обрыве в записи будет пауза в несколько секунд; если источник не восстановился за `RECONNECT_WINDOW`, ответ закрывается (окно может быть превышено примерно на `STARTUP_TIMEOUT` + 30 с).

Ссылки: [Streamlink](https://github.com/streamlink/streamlink), [Gluetun](https://github.com/qdm12/gluetun).

Используйте поток только там, где у вас есть право и доступ к трансляции. Проект не ломает DRM, не имитирует подписку и не перепубликует поток в интернет по умолчанию.
