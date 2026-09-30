// Calendar-month navigation for the Metrics page. The server owns bounds and
// translates the selected month into its available date range.
(function () {
  'use strict';
  var root = document.querySelector('.metrics-month-nav');
  if (!root) return;
  var current = root.dataset.month;
  var min = root.dataset.minMonth;
  var max = root.dataset.maxMonth;
  var select = root.querySelector('[data-month-select]');

  function date(value) { return new Date(value + '-01T12:00:00'); }
  function value(d) { return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0'); }
  function navigate(next) {
    var url = new URL(window.location.href);
    url.searchParams.set('month', next);
    url.searchParams.delete('from');
    url.searchParams.delete('to');
    window.location.assign(url.toString());
  }
  function label(d) { return d.toLocaleDateString('en-CA', { month: 'long', year: 'numeric' }); }

  for (var d = date(min); d <= date(max); d = new Date(d.getFullYear(), d.getMonth() + 1, 1)) {
    var option = document.createElement('option');
    option.value = value(d);
    option.textContent = label(d);
    option.selected = option.value === current;
    select.appendChild(option);
  }
  root.addEventListener('click', function (event) {
    var button = event.target.closest('[data-month-step]');
    if (!button) return;
    var next = new Date(date(current).getFullYear(), date(current).getMonth() + Number(button.dataset.monthStep), 1);
    var nextValue = value(next);
    if (nextValue >= min && nextValue <= max) navigate(nextValue);
  });
  select.addEventListener('change', function () { navigate(select.value); });
  var buttons = root.querySelectorAll('[data-month-step]');
  for (var i = 0; i < buttons.length; i++) {
    var candidate = new Date(date(current).getFullYear(), date(current).getMonth() + Number(buttons[i].dataset.monthStep), 1);
    buttons[i].disabled = value(candidate) < min || value(candidate) > max;
  }
}());
