# AGENTS.md

このファイルは、FlexRAID の実装・変更時に守る開発規約と、実装上の不変条件を定義する。利用者向けの概要・利用方法・サポート範囲は `README.md` を参照する。

仕様の変更や、README とこのファイルの記述に矛盾を見つけた場合は、どちらかを暗黙に優先して実装を進めない。コード・テスト・両ドキュメントを確認し、正しい契約を確定して両方を同じ変更で整合させる。未確定事項は推測で固定せず、未決であることを明示する。

## 1. 基本原則

- レイヤーの責務と依存方向を守る。上位レイヤーは下位レイヤーの公開 API を利用し、下位レイヤーは上位レイヤーの内部実装を知らない。
- 仕様・契約を満たすために必要な最小限の抽象化を行う。将来の可能性だけを理由に汎用化しない。
- エラーを握りつぶさず、呼び出し元が操作結果を判断できる形で返す。
- 既存データを誤って破壊する可能性がある変更では、失敗時・中断時・再起動後の状態まで検討する。
- 実装変更には、外部から観測できる振る舞いを確認するテストを追加・更新する。
- コードとテストを変更する場合は、作業開始前に default branch のこのファイルを読む。

## 2. パッケージとファイル構成

主要なパッケージは次のとおり。

- `io/`: 物理ファイル操作
- `distribution/`: ストライプ、チャンク、冗長化、復旧、物理配置
- `orchestration/`: Header、論理 Object、Namespace、ファイル offset、配置方針
- `fuse/`: go-fuse と Orchestration のアダプター
- `pkg/shared/`: レイヤー間で共有する型・定数
- `storage/`: 物理保存形式の共通型
- `main/`: 設定読み込みとマウント起動

- パッケージ境界は責務の境界とする。サブパッケージは明確な責務上の理由がある場合に追加する。
- ファイル名は小文字とし、必要に応じて単語をアンダースコアで区切る。
- ファイルは操作または明確な機能・責務ごとに分ける。責務が曖昧な `utils.go`、`helpers.go`、`common.go` を安易に作らない。
- 公開操作は操作ごとの関数として定義し、複数操作を単一の `Execute` などで振り分けない。
- 操作の実装ファイルには、原則としてその操作の Request 型、Response 型、公開関数を置く。操作固有の補助処理は同じファイルに置き、複数操作で共有する処理は責務が明確なファイルへ置く。
- 公開識別子は PascalCase、非公開識別子は camelCase とする。

## 3. 型と共通定義

- 複数レイヤーで共有する型は `pkg/shared` に定義し、各レイヤーから直接参照する。各レイヤーで型エイリアスとして再公開したり、同じ意味の型を重複定義したりしない。
- レイヤー固有の意味・責務を持つ型は、そのレイヤー内に定義する。
- 複数箇所で使用する上限値・既定値・単位定義は `pkg/shared/config` を唯一の正とし、利用箇所に数値を重複定義しない。
- 公開 API の Request / Response 型は、原則として操作ごと・レイヤーごとに定義する。名前が同じという理由だけで異なるレイヤー間の型を共有しない。
- 実行時の公開 API の Request には `context.Context` を含め、呼び出し元からキャンセル・期限・リクエストスコープの値を伝播できるようにする。初期化専用など、実行時要求ではない API は必要性に応じて判断する。
- 公開 API の Response は、その操作の結果として必要な値だけを返す。内部 map / slice / pointer をそのまま公開して呼び出し側から内部状態を変更できる形にしない。必要に応じてコピーまたは読み取り専用の値へ変換する。
- エラーは呼び出し元が判断できる形で返し、上位層で文脈を追加する場合は `%w` 等で元のエラーを保持する。エラー文字列だけを比較して分岐しない。

## 4. Object と Namespace の不変条件

### ObjectID / ParentID

- `ObjectID` は有効な UUIDv4 とする。生成・文字列解析・バイナリ解析など、外部入力との境界で検証する。
- ObjectID のゼロ値を root、未設定、無効値などの特別な識別子として使用しない。
- nullable な識別子は、ゼロ UUID で代用せず、存在フラグなどで有無を明示する。
- `ParentID` は nullable。root Directory と Namespace に直接所属しない File は nil を持ち得る。
- root は ParentID が nil の唯一の Directory Object として識別する。ParentID が nil の File を root と誤認しない。

### ObjectKind / HardLink / Symlink

- 論理 Object の Kind は Orchestration が所有し、File、Directory、Symlink、HardLink を区別する。
- Directory の children は永続化せず、Object の ParentID / Name から起動時にメモリ上へ構築する。
- File はデータ実体を保持する。直接の Namespace 名を持つ場合は ParentID / Name を持ち、HardLink 経由だけで参照される場合は ParentID が nil になり得る。
- HardLink は Namespace 上の名前を表す Object で、ParentID と File 実体を直接指す TargetID を持つ。HardLink 自身はデータを保持しない。
- HardLink の連鎖、Directory / Symlink への HardLink、Directory 自体の HardLink は認めない。
- HardLink 名は参照先 File と同じ Runtime ファイル実体・属性・inode identity を共有する。名前の追加・削除・移動は、対象パスの Namespace entry に対して行い、別名を誤って削除しない。
- File の直接名が削除されても HardLink が残る場合は File 実体を保持する。最後の Namespace 参照がなくなった場合も、open handle が残っていれば handle の寿命が終わるまで実体を利用可能にする。
- Symlink のリンク先文字列は通常の論理データとして保持し、Header に格納しない。リンク自身の属性はリンク自身の属性として返し、ReadLink は保存されたリンク先文字列を返す。パス追跡・絶対/相対パス解決は POSIX / FUSE のパス解決に委ねる。
- NLink は永続化属性にしない。現在の Namespace と HardLink の参照関係から Orchestration が導出し、FUSE は返された値を使用する。

### Namespace 更新

- Directory entry の追加・削除・移動が成功したら、影響を受ける親 Directory の Mtime と Ctime を同じ操作時刻で更新・永続化する。
- 対象は Create / Mknod / Mkdir / Symlink / Link / Unlink / Rmdir / Rename。Rename が同一親なら一度、異なる親なら移動元・移動先の両方を更新する。
- 失敗した操作および変更のない Rename では親時刻を更新しない。
- ファイル内容や子 Object の属性変更では親 Directory の時刻を更新しない。
- 親時刻更新は Orchestration の Namespace 更新経路で行い、FUSE callback で独自に更新しない。
- Namespace の変更と NLink の導出元は同じ親子関係・参照関係とする。NLink の数値を個別に加減算して永続化しない。
- 複数 Header を更新する Namespace 操作について、クラッシュ時の完全な原子性を保証できない場合は、その制約を隠さず明示する。部分更新を成功状態として誤認させない。

### 永続化モデルと Namespace の対応

Namespace は独立したツリーやデータベースとして保存せず、各 Object の Header を正として起動時に再構築する。

| 論理要素 | 永続化単位 | 永続化する情報 | メモリ上だけに保持する情報 |
|---|---|---|---|
| root Directory | 通常の Directory Object | 有効な ObjectID、Kind、ParentID=nil、属性 | children |
| Directory | Directory Object | ObjectID、Kind、ParentID、Name、属性 | children |
| File | File Object | ObjectID、Kind、ParentID（直接の名前がなければ nil）、Name、属性、および Distribution が管理するデータストライプ | Runtime 状態、open handle、キャッシュ |
| HardLink | 独立した HardLink Object | ObjectID、Kind、ParentID、Name、TargetID | TargetID から解決した File Runtime への参照 |
| Symlink | Symlink Object | ObjectID、Kind、ParentID、Name、属性。リンク先文字列は Header ではなく通常の論理データ | Runtime 状態 |

- ObjectID は永続化された Object の識別子であり、Namespace の親子関係は Header の ParentID / Name で表す。
- Directory の children、HardLink の解決済み参照、NLink、Namespace の検索用構造など、永続 Header / データから再構築できる情報を独立した永続データとして重複保存しない。
- File の実データは File Object に一つだけ保持する。HardLink Object は名前と TargetID のみを保持し、データストライプを複製しない。
- 起動時は永続 Object を読み込み、Header を検証したうえで ParentID / Name から Directory children を構築し、HardLink の TargetID を File に解決する。構築後の Namespace と Runtime の参照関係が、実行中の論理状態となる。
- Namespace の追加・削除・移動では、対象 Object の Header に保持する ParentID / Name 等を更新する。Directory children を永続化したり、親 Directory に子一覧を別途書き込んだりしない。
- File の属性変更は該当 Header を更新し、ファイル内容の変更は Distribution のストライプ API を通じてデータを更新する。サイズ変更で配置計画が変わる場合の移行規則は Orchestration の責務に従う。
- HardLink の追加・削除は HardLink Object 自身の永続化・削除と、参照先 File の ParentID / Deleted 状態の更新で表現する。参照先 File のデータを HardLink ごとに複製しない。
- Namespace 操作が複数 Header の更新を伴う場合、更新順序と失敗時の状態を考慮する。複数 Header をまたぐトランザクションやクラッシュ時の完全な原子性は保証されないため、途中状態を成功として扱ったり、未完了の更新を隠したりしない。
- 論理削除は Header の Deleted 状態で表し、物理削除は Cleanup に委ねる。Namespace から見えなくなったことだけを理由に、open handle が利用中の File 実体や不整合保護対象を削除してはならない。

## 5. レイヤーの責務

### I/O

- 指定された物理 root 上で物理ファイルの読み書き・同期・削除・列挙を行う。
- Header の解析、チャンクやストライプの意味解釈、配置方針、パリティ計算、復旧判断を行わない。
- `ScanHeaders` は StripeID 0 の物理ファイル列挙、`ListChunks` は全ストライプの物理ファイル列挙を行う。どちらも Header / Chunk の意味を解析しない。

### Distribution

- 論理ストライプ単位で読み書きする。論理ファイル offset、ファイルサイズ、Namespace、Header の形式を扱わない。
- `WriteStripe` は指定された Layout / ChunkSize / IO 配置方針に従ってストライプをチャンクへ分割し、必要な冗長データを生成して永続化する。ファイル内 offset の分割や既存ストライプとのマージは行わない。
- ストライプのバージョン・チャンク・物理配置情報を管理し、起動時に I/O の全件列挙結果からメモリ上へ再構築する。永続インデックスを別途持たない。
- ストライプ書き込みは、対象ストライプの全チャンクの書き込みが完了してから成功を返す。失敗した新しい書き込みを有効バージョンとして公開せず、既存の有効データを維持する。
- 復旧はチャンクの欠落または物理読み取りエラーにより取得できない場合を対象とする。読み取れたチャンク同士の内容比較やパリティ不一致だけを理由にデータを訂正しない。
- 初期パリティ方式は XOR 1 パリティであり、1 データチャンクの欠損まで復元できる。内容改ざん・サイレント破損の検出を保証しない。
- 配置ポリシーは決定せず、Orchestration から渡された配置方針を実際のチャンク配置に反映する。

### Orchestration

- Header の定義・生成・解析・シリアライズ・更新、論理 Object と Namespace、ファイル offset と StripeID / stripe 内 offset の変換、配置方針を担当する。
- Distribution の API は論理ストライプ単位で利用する。チャンク番号、StripeVersion、物理配置の内部表現を持ち込まない。
- 通常の書き込みは変更対象ストライプだけを読み取り・変更・書き戻す。通常書き込みのためにファイル全体を RAM に読み込んで再構築しない。
- ファイルサイズ変更などで配置計画が変わる場合は、必要な既存データを新しいストライプ境界・チャンクサイズへ移行する。サイズを変えない上書きでは配置計画を再計算しない。
- Stripe 0 の先頭 1 KiB は Header 予約領域。Stripe 0 の物理チャンクサイズは 4 KiB 固定。論理データチャンク数を N とすると Stripe 0 のファイル本体容量は `4 KiB × N - 1 KiB`。
- Stripe 1 以降のチャンクサイズ C は Stripe 0 の Header に保持し、通常ストライプの論理容量は `C × N`。ファイル offset から StripeID / stripe 内 offset への変換はこの geometry に従う。
- Namespace 初期化では Header を読み込み・検証し、ParentID / Name からツリーを構築し、HardLink を解決してから全体を検査する。root を一意に復元できない不整合は初期化エラー。一部 Object の不整合は Namespace から除外して warning を記録し、関連データを誤って Cleanup しないよう保護する。
- 配置先の選択は容量・利用可能状態・ファイルの配置計画に基づき Orchestration が行う。既存ストライプは Distribution が保持する配置情報を再利用する。

### FUSE

- go-fuse v2.12.0 の callback 契約に従い、OS からの操作を Orchestration API へ変換する。Namespace や論理属性の独自キャッシュを持たない。
- FUSE は物理ファイル・チャンク・パリティ・配置方針を直接扱わない。
- regular file の Read / Write は有効な Orchestration FileHandle を必須とする。独立した Open ごとに handle を作り、dup は同じ open-file description / handle を共有する。
- Read / Write の offset は kernel から渡された値を正とし、handle 内の cursor を位置の正として扱わない。
- Dirty stripe cache は FileHandle ごとではなく Runtime file 実体ごとに共有し、HardLink 間でも同じ cache を参照する。
- Flush は複数回呼ばれ得るため反復可能とし、handle を解放しない。Fsync は pending dirty data の永続化を保証する。Release は最終的な handle cleanup とし、失敗し得る永続化を Release だけに依存させない。
- FUSE の `default_permissions` に OS の権限判定を委ねる。FlexRAID 独自の permission 判定を重複実装しない。
- Advisory lock forwarding、Direct I/O、KEEP_CACHE、ExplicitDataCacheControl、parallel direct writes は、対応する整合性契約とテストが揃うまで有効化しない。

## 6. 制約値とファイルシステム属性

- 共有制約値は `pkg/shared/config` を唯一の正とする。現在の主要な制約には、最大ファイルサイズ 16 TiB、最大名前長 255 bytes、BlockSize 4 KiB、Header 予約領域 1 KiB がある。
- 単位を混同しない。I/O 容量は bytes、StatFs のブロック数は BlockSize 単位とする。
- StatFs は Orchestration が組み立て、FUSE は Linux statfs response へ写像する。物理総容量・空き容量と、冗長化を考慮した論理配置可能容量を区別する。
- FUSE の NLink / inode identity / mode / ownership / timestamps / size は Orchestration の論理属性と一貫させる。

## 7. 排他制御

- 公開 Orchestration 操作が必要な排他制御を取得し、非公開の `*Locked` 処理は呼び出し元が必要なロックを保持していることを前提とする。
- ロック取得・解放を内部関数へ分散させない。ロック取得済みの公開操作から別の公開操作を呼ばず、共有処理は非公開関数へ切り出す。
- ロック取得前に対象を調査した場合は、ロック取得後に対象と前提条件を再検証する。
- go-fuse の SyncRead などの mount option を内部同期の代替にしない。異なる handle、HardLink、read/write、metadata 操作間の整合性は Orchestration が保証する。

### 公開 API と非公開処理のロック境界

- 排他制御を所有するレイヤーでは、外部から呼び出される公開操作関数が必要なロックを取得し、処理終了時に解放する。ロック取得・解放の責務を複数の内部関数へ分散させない。
- Orchestration の公開操作は、原則としてロック取得後に非公開の `*Locked` 関数へ処理を委譲する。非公開の `*Locked` 関数は、呼び出し元が必要なロックを保持していることを前提とし、自身ではロックを取得・解放しない。
- 公開操作関数から別の公開操作関数を呼び出してはならない。複数操作で共有する処理は非公開関数へ切り出し、各公開操作関数が必要なロックを取得したうえで、その非公開関数を呼び出す。
- ロックを必要とする非公開関数には、保護対象と必要なロックをコメント等で明記する。`*Locked` という名前だけを前提条件の説明の代わりにしない。
- ロック取得前に Namespace や参照関係を調べてロック対象を決めた場合、取得後に対象と前提条件を再検証する。ロック集合が不足または不適切なら、安全に解放して取得し直す。
- FUSE callback は go-fuse から呼ばれるアダプターであり、Namespace やファイル状態を保護する Orchestration のロックを独自に取得しない。状態変更・整合性制御は Orchestration の公開 API に集約する。
- FUSE callback 内で複数の Orchestration 公開 API を呼び出しても、それらをまたぐ原子性は得られない。複数 API を一つの不可分な操作として扱う必要がある場合は、FUSE 側でロックしてつなぎ合わせず、必要なロックを管理する Orchestration の公開操作として実装する。
- go-fuse の mount option や callback の呼び出し順序を、Orchestration 内部の排他制御の代替にしない。異なる handle、HardLink、read/write、metadata 操作間の整合性は Orchestration が保証する。

## 8. ログ

- Go 標準ライブラリ `log/slog` を使用する。
- 公開操作の開始・完了、処理件数、所要時間、結果要約など通常処理の記録は DEBUG とする。
- WARNING は処理を継続できるが運用者が把握すべき例外的状況、ERROR はデータ整合性や可用性に実害を及ぼす重大な問題に限る。
- error を返すことだけを理由に WARNING / ERROR を出さない。呼び出し側で通常処理として扱えるエラーは原則 DEBUG とする。
- 同じ問題を下位層・上位層で重複記録しない。ログレベルは実行時に選択可能とする。
- データ本体、秘密情報、不要に大きな値をログへ出さない。

## 9. テストと品質ゲート

- 実装した操作・機能には対応する単体 / 統合テストを用意し、内部実装だけでなく公開 API と外部から観測できる振る舞いを検証する。
- Go テストは `go test ./...` で実行できる状態を維持する。
- Go ソースを変更・生成したら `gofmt -w` を実行し、続けて `gofmt -l` の出力が空であることを確認する。フォーマット確認はコミット前の必須条件とする。
- POSIX 互換性の回帰確認には、リポジトリで固定した pjdfstest revision と FlexRAID 専用 skiplist を使用する。
- pjdfstest の SKIP は v1 でサポートしない機能に限る。実装済み機能の不具合や単に未実装なだけの機能を隠す目的で追加しない。
- skiplist は `tools/pjdfstest-flexraid-skiplist.tsv` を唯一の正とし、ケース単位で理由を明記する。pjdfstest 更新時は SKIP 対象のテスト内容を再確認する。
- テスト失敗を修正するときは、最初の失敗原因をログと assertion から特定し、関連する回帰テストを追加する。

## 10. Pull Request 運用

- 作業開始時と新規 PR 作成前に Open PR を確認する。
- 今回の変更に関連する Open PR があれば、そのブランチへ追加コミットする。無関係な PR がある場合のみ独立 PR を作成する。
- PR は変更範囲・設計判断・テスト結果を明記する。実装コードを変更した場合は、関連テストと CI 結果を確認する。
- 変更対象外のファイルを整形・更新したり、無関係な変更を同じ PR に混ぜたりしない。
