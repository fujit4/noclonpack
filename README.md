# noclonpack

**noclonpack**は、Gitを使用せずにVimのプラグインを管理するためのツールです。Gitが制限された環境でも、zipファイルのダウンロードと展開により、プラグインの追加・削除・同期を行うことができます。

## 特徴

* **Git不要**: `git clone`を使用せず、zipファイルのダウンロードと展開でプラグインを管理します。
* **プロキシ対応**: `http_proxy`、`https_proxy`、`no_proxy`の環境変数を利用して、プロキシ環境下でも動作します。
* **neovim標準の`packpath`を活用**: Vim 8以降で導入された`packpath`を使用し、プラグインを`start`または`opt`ディレクトリに配置します。

## インストール

Release ページからダウンロードしてください。


## 使い方

### プラグイン定義ファイル

プラグインの情報は`noclonpack_plugins.yml`ファイルに記述します。このファイルには、`start`および`opt`の各セクションにプラグインの情報をリスト形式で記述します。

```yml
start:
  - repo: username/repo1
    url: https://github.com/username/repo1/archive/refs/tags/v1.0.0.zip
  - repo: username/repo2
    url: https://github.com/username/repo2/archive/refs/heads/main.zip
opt:
  - repo: username/repo3
    url: https://github.com/username/repo3/archive/refs/heads/main.zip
```

ファイルはinit.luaと同階層に格納します。
`XDG_CONFIG_HOME`の指定がある場合は、`${XDG_CONFIG_HOME}/nvim/`。
指定がない場合は、Windowsの場合、`${LOCALAPPDATA}/nvim/`、それ以外の場合は、`~/.config/nvim/`。


### コマンド一覧

#### `noclonpack sync`

`noclonpack_plugins.yml`の内容に基づいて、プラグインの同期を行います。不要になったプラグインは削除され、必要なプラグインは指定されたURLからzipファイルをダウンロードして展開されます。


#### `noclonpack add <dir> <url>`

* `<dir>`: `start`または`opt`を指定します。
* `<url>`: プラグインのzipファイルのURLを指定します。

GitHubのzipダウンロードURLを指定した場合、リポジトリ名を解析して`noclonpack_plugins.yml`を自動的に更新します。GitHub以外のURLの場合は、手動でファイルを更新してください。


#### `noclonpack rm <repo>`

指定したリポジトリ名のプラグインを`noclonpack_plugins.yml`から削除します。プラグイン自体をpackpathディレクトリから削除するには、`noclonpack sync`を実行してください。


#### `noclonpack list`

`noclonpack_plugins.yml`の内容を標準出力します。


#### `noclonpack version`

`noclonpack_plugins.yml`のversionを表示します。


