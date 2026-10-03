# Moodle/WAF-safe HTML profile

Экспериментально проверенный стандарт проекта:

**HTML-фрагмент + inline CSS + таблицы + статический SVG + `<pre>/<code>` + `<details>/<summary>` + простой iframe записи.**

## Разрешено

- `<div>`, `<p>`, заголовки;
- inline `style=""`;
- flex/grid;
- таблицы;
- `<pre><code>`;
- статический `<svg>`;
- `<details>/<summary>`;
- `<iframe>` записи без JS-обвязки.

## Запрещено

- `<script>`;
- `<style>`;
- `javascript:`;
- `onclick`, `onload`, `onerror` и любые `on...`;
- `srcdoc`;
- `data:text/html`;
- `<object>`, `<embed>`, `<applet>`;
- JS-библиотеки;
- локальные `file:///`, `Z:\...`, `C:\...`, `./files/...`;
- `width:100vw`;
- активные SVG-конструкции `animate`, `animateTransform`, `foreignObject`.

## Python-код

Python внутри `<pre>` не является проблемой, если это текст.

HTML-символы в примерах необходимо экранировать:

```html
<pre><code>if x &lt; 10:
    print("OK")</code></pre>
```

## iframe

Запись берётся только из `Запись.txt`.

Не добавлять JS resize/autoplay wrapper.

## Анимация

Если педагогически нужна «анимация», использовать:

- несколько статических кадров;
- timeline;
- before/after;
- пошаговую SVG-схему;
- стрелки процесса.

Такой профиль существенно снижает риск ответа Moodle/WAF «подозрительная активность».
