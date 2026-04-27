# CLI Proxy API

[English](README.md) | 한국어 | [中文](README_CN.md) | [日本語](README_JA.md)

CLI용 OpenAI/Gemini/Claude/Codex 호환 API 인터페이스를 제공하는 프록시 서버입니다.

OAuth를 통해 OpenAI Codex(GPT 모델)와 Claude Code도 지원합니다.

로컬 또는 멀티 계정 CLI 접근을 OpenAI(Responses 포함)/Gemini/Claude 호환 클라이언트 및 SDK로 사용할 수 있습니다.

## 개요

- CLI 모델용 OpenAI/Gemini/Claude/Codex 호환 API 엔드포인트
- OAuth 로그인 기반 OpenAI Codex 지원(GPT 모델)
- OAuth 로그인 기반 Claude Code 지원
- provider routing 기반 Amp CLI 및 IDE 확장 지원
- 스트리밍 및 비스트리밍 응답 지원
- 함수 호출 / 도구 지원
- 멀티모달 입력(텍스트, 이미지) 및 OpenAI 호환 이미지 생성/편집 엔드포인트 지원 (`/v1/images/generations`, `/v1/images/edits`)
- Gemini, OpenAI, Claude 다중 계정 라운드로빈 로드밸런싱
- 간단한 CLI 인증 플로우(Gemini, OpenAI, Claude)
- Generative Language API Key 지원
- AI Studio Build 멀티 계정 로드밸런싱
- Gemini CLI 멀티 계정 로드밸런싱
- Claude Code 멀티 계정 로드밸런싱
- OpenAI Codex 멀티 계정 로드밸런싱
- 설정을 통한 OpenAI 호환 업스트림 제공자 지원(예: OpenRouter)
- 프록시 임베딩용 재사용 가능한 Go SDK (`docs/sdk-usage.md` 참고)

## 시작하기

CLIProxyAPI 가이드: [https://help.router-for.me/](https://help.router-for.me/)

## Management API

[MANAGEMENT_API.md](https://help.router-for.me/management/api)를 참고하세요.

- 클라이언트별 출력 토큰 할당량 정책 문서: [docs/client-api-key-quota-policies.md](docs/client-api-key-quota-policies.md)
- `client-api-key-policies`, `output-token-quota-reset-hours`, `GET /v0/management/usage` 기반 quota 확인 방법도 위 문서를 참고하세요.

## Amp CLI 지원

CLIProxyAPI는 [Amp CLI](https://ampcode.com) 및 Amp IDE 확장을 내장 지원하므로, Google/ChatGPT/Claude OAuth 구독을 Amp 코딩 도구와 함께 사용할 수 있습니다.

- Amp API 패턴용 provider route alias (`/api/provider/{provider}/v1...`)
- OAuth 인증 및 계정 기능을 위한 management proxy
- 자동 라우팅 기반 스마트 모델 fallback
- 보안 우선 설계: management endpoint는 localhost 중심으로 보호

특정 백엔드 계열의 요청/응답 형태가 필요할 때는 합쳐진 `/v1/...` 엔드포인트 대신 provider-specific 경로를 우선 사용하세요.

- messages 계열 백엔드: `/api/provider/{provider}/v1/messages`
- 모델 단위 generate 계열 엔드포인트: `/api/provider/{provider}/v1beta/models/...`
- chat-completions 계열 백엔드: `/api/provider/{provider}/v1/chat/completions`
- OpenAI 스타일 이미지 생성/편집 백엔드: `/api/provider/{provider}/v1/images/generations`, `/api/provider/{provider}/v1/images/edits`

## 이미지 엔드포인트 안내

- `/v1/images/generations` 는 **텍스트 프롬프트만으로 새 이미지를 생성**하는 엔드포인트입니다.
- `/v1/images/edits` 는 **기존 이미지와 프롬프트를 함께 보내 이미지를 수정**하는 엔드포인트입니다.
- `generations` 는 보통 `prompt`, `model`, `size`, `quality`, `response_format` 같은 JSON 필드를 사용합니다.
- `edits` 는 JSON(`images[].image_url`) 또는 `multipart/form-data`(`image`, `image[]`, optional `mask`)를 사용할 수 있습니다.
- 두 엔드포인트 모두 기본 응답은 보통 `data[].b64_json` 이며, `revised_prompt`, `size`, `quality`, `usage` 같은 메타데이터가 함께 내려올 수 있습니다.

## SDK 문서

- 사용법: [docs/sdk-usage.md](docs/sdk-usage.md)
- 고급(실행기와 번역기): [docs/sdk-advanced.md](docs/sdk-advanced.md)
- 접근 제어: [docs/sdk-access.md](docs/sdk-access.md)
- 워처: [docs/sdk-watcher.md](docs/sdk-watcher.md)
- 커스텀 Provider 예시: `examples/custom-provider`
