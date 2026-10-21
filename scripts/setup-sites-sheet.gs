/**
 * Turns the first tab of this spreadsheet into the redirect list, with the
 * same guard rails Airtable gave: URL validation, Active checkboxes, and
 * highlighting for incomplete or duplicate rows.
 *
 * How to use:
 *   1. Create a blank Google Sheet (or one with your imported Airtable CSV:
 *      File → Import → Replace current sheet).
 *   2. Extensions → Apps Script, paste this file, click Run (setupSitesSheet),
 *      and approve the permission prompt (it only touches this spreadsheet).
 *
 * Safe to run more than once. It keeps existing rows; on an empty sheet it
 * adds two example rows.
 */
function setupSitesSheet() {
  const sheet = SpreadsheetApp.getActiveSpreadsheet().getSheets()[0];
  sheet.setName('Sites');

  // Header row (only if the sheet is empty).
  if (sheet.getLastRow() === 0) {
    sheet.getRange('A1:C3').setValues([
      ['Name', 'URL', 'Active'],
      ['Example A', 'https://example.com/a', 'TRUE'],
      ['Example B', 'https://example.com/b', 'FALSE'],
    ]);
  }
  const header = sheet.getRange(1, 1, 1, sheet.getLastColumn()).getValues()[0].map(String);
  const col = (name) => {
    const i = header.indexOf(name);
    if (i < 0) throw new Error('Missing "' + name + '" column in row 1');
    return i + 1;
  };
  const urlCol = col('URL'), activeCol = col('Active'), nameCol = col('Name');
  const urlA1 = columnLetter(urlCol), activeA1 = columnLetter(activeCol), nameA1 = columnLetter(nameCol);

  sheet.getRange(1, 1, 1, header.length).setFontWeight('bold');
  sheet.setFrozenRows(1);
  // Warn (not block) before anyone edits the header, since the function finds columns by it.
  sheet.getRange('1:1').protect().setDescription('Column headers used by RandomRedirect').setWarningOnly(true);

  const lastRow = Math.max(sheet.getLastRow(), 2);
  const bodyRows = Math.max(sheet.getMaxRows() - 1, lastRow - 1);

  // Active: real checkboxes. Airtable's CSV export writes "checked", so
  // convert existing values before inserting checkboxes (which resets cells).
  const activeRange = sheet.getRange(2, activeCol, bodyRows, 1);
  const ticked = activeRange.getValues().map(([v]) =>
    [v === true || ['true', 'checked', 'yes', '1'].includes(String(v).trim().toLowerCase())]);
  activeRange.insertCheckboxes();
  activeRange.setValues(ticked);

  // URL: reject anything that isn't a valid URL.
  sheet.getRange(2, urlCol, bodyRows, 1).setDataValidation(
    SpreadsheetApp.newDataValidation()
      .requireTextIsUrl()
      .setAllowInvalid(false)
      .setHelpText('Enter a full URL, e.g. https://example.com/page')
      .build());

  // Highlighting.
  const all = sheet.getRange(2, 1, bodyRows, header.length);
  const urlRange = sheet.getRange(2, urlCol, bodyRows, 1);
  const r = (f) => f.replace(/URL/g, '$' + urlA1 + '2').replace(/ACTIVE/g, '$' + activeA1 + '2').replace(/NAME/g, '$' + nameA1 + '2');
  sheet.setConditionalFormatRules([
    // Red: ticked Active but no URL (would be ignored by the function).
    SpreadsheetApp.newConditionalFormatRule()
      .whenFormulaSatisfied(r('=AND(ACTIVE=TRUE, LEN(URL)=0)'))
      .setBackground('#f4c7c3').setRanges([all]).build(),
    // Orange: the same URL appears more than once.
    SpreadsheetApp.newConditionalFormatRule()
      .whenFormulaSatisfied(r('=AND(LEN(URL)>0, COUNTIF($' + urlA1 + ':$' + urlA1 + ', URL)>1)'))
      .setBackground('#fce8b2').setRanges([urlRange]).build(),
    // Yellow: a URL with no Name.
    SpreadsheetApp.newConditionalFormatRule()
      .whenFormulaSatisfied(r('=AND(LEN(URL)>0, LEN(NAME)=0)'))
      .setBackground('#fff2cc').setRanges([sheet.getRange(2, nameCol, bodyRows, 1)]).build(),
    // Grey text: inactive rows.
    SpreadsheetApp.newConditionalFormatRule()
      .whenFormulaSatisfied(r('=AND(LEN(URL)>0, ACTIVE=FALSE)'))
      .setFontColor('#9aa0a6').setRanges([all]).build(),
  ]);

  sheet.autoResizeColumns(1, header.length);
}

function columnLetter(n) {
  let s = '';
  for (; n > 0; n = Math.floor((n - 1) / 26)) s = String.fromCharCode(65 + ((n - 1) % 26)) + s;
  return s;
}
